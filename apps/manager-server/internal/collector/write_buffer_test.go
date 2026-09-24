package collector

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func bufferTestPayload(requestID string, second int) string {
	return fmt.Sprintf(`{
		"request_id":%q,
		"timestamp":"2026-05-06T00:00:%02dZ",
		"provider":"codex",
		"model":"gpt-test",
		"endpoint":"POST /v1/chat/completions",
		"input_tokens":1,
		"output_tokens":2
	}`, requestID, second)
}

func TestManagerBuffersUsageEventsWhileDatabaseIsLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager := NewManager(testConfig(t, "http"), db)
	handler := &recordingUsageHandler{}
	manager.SetUsageEventHandler(handler)
	ctx := context.Background()

	locker, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open locker: %v", err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	lock, err := locker.Conn(ctx)
	if err != nil {
		t.Fatalf("locker conn: %v", err)
	}
	if _, err := lock.ExecContext(ctx, `begin immediate`); err != nil {
		t.Fatalf("take write lock: %v", err)
	}

	if err := manager.processItems(ctx, RuntimeConfig{}, []string{bufferTestPayload("locked-1", 1)}); err != nil {
		t.Fatalf("process while locked: %v", err)
	}
	// Inside the retry window the buffer must not wait on the lock again, or a
	// Pub/Sub consumer would stall once per message.
	started := time.Now()
	if err := manager.processItems(ctx, RuntimeConfig{}, []string{bufferTestPayload("locked-2", 2)}); err != nil {
		t.Fatalf("process inside retry window: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("process inside retry window took %s, want no database wait", elapsed)
	}
	if status := manager.Status(); status.PendingEvents != 2 || status.TotalInserted != 0 || len(handler.events) != 0 {
		t.Fatalf("while locked: status=%#v handler=%d, want 2 pending and nothing inserted", status, len(handler.events))
	}

	if _, err := lock.ExecContext(ctx, `rollback`); err != nil {
		t.Fatalf("release write lock: %v", err)
	}
	manager.buffer.mu.Lock()
	manager.buffer.retryAt = time.Time{}
	manager.buffer.mu.Unlock()
	if err := manager.writeEvents(ctx, RuntimeConfig{}, nil); err != nil {
		t.Fatalf("idle flush: %v", err)
	}

	status := manager.Status()
	if status.PendingEvents != 0 || status.TotalInserted != 2 || len(handler.events) != 2 {
		t.Fatalf("after unlock: status=%#v handler=%d, want 2 inserted", status, len(handler.events))
	}
	if handler.events[0].RequestID != "locked-1" || handler.events[1].RequestID != "locked-2" {
		t.Fatalf("flushed order = %q, %q", handler.events[0].RequestID, handler.events[1].RequestID)
	}
}

func TestWriteBufferDropsOldestEventsPastLimit(t *testing.T) {
	manager := NewManager(testConfig(t, "http"), nil)
	manager.buffer.retryAt = time.Now().Add(time.Hour)
	events := make([]usage.Event, maxBufferedEvents+3)
	for i := range events {
		events[i].EventHash = fmt.Sprintf("event-%d", i)
	}
	if err := manager.writeEvents(context.Background(), RuntimeConfig{}, events); err != nil {
		t.Fatalf("buffer events: %v", err)
	}
	buffered := manager.buffer.events
	if len(buffered) != maxBufferedEvents || manager.buffer.dropped != 3 || buffered[0].EventHash != "event-3" {
		t.Fatalf("buffered=%d dropped=%d first=%q, want the 3 oldest dropped", len(buffered), manager.buffer.dropped, buffered[0].EventHash)
	}
}

func TestManagerFlushesBufferedUsageEventsOnExit(t *testing.T) {
	db := newTestStore(t)
	manager := NewManager(testConfig(t, "http"), db)
	event, err := usage.NormalizeRaw([]byte(bufferTestPayload("exit-1", 3)))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	manager.buffer.events = []usage.Event{event}
	manager.buffer.retryAt = time.Now().Add(time.Hour)

	manager.flushOnExit(RuntimeConfig{})

	if pending := manager.bufferedEvents(); pending != 0 {
		t.Fatalf("pending after exit flush = %d, want 0", pending)
	}
	if status := manager.Status(); status.TotalInserted != 1 {
		t.Fatalf("status after exit flush = %#v, want 1 inserted", status)
	}
}

func TestWriteBufferDrainsBacklogInChunks(t *testing.T) {
	db := newTestStore(t)
	manager := NewManager(testConfig(t, "http"), db)
	backlog := make([]usage.Event, 0, 2*flushChunkEvents+500)
	for i := 0; i < cap(backlog); i++ {
		event, err := usage.NormalizeRaw([]byte(bufferTestPayload(fmt.Sprintf("backlog-%d", i), i%60)))
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		backlog = append(backlog, event)
	}
	manager.buffer.events = backlog

	if err := manager.writeEvents(context.Background(), RuntimeConfig{}, nil); err != nil {
		t.Fatalf("drain backlog: %v", err)
	}
	if status := manager.Status(); status.PendingEvents != 0 || status.TotalInserted != int64(len(backlog)) {
		t.Fatalf("status after drain = %#v, want %d inserted", status, len(backlog))
	}
}
