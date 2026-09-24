package collector

import (
	"context"
	"log"
	"sync"
	"time"

	codexinspectionrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/codexinspection"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

const (
	// maxBufferedEvents bounds the events held while the database is locked:
	// hours of production traffic at a few events per second.
	maxBufferedEvents = 50000
	bufferRetryMin    = time.Second
	bufferRetryMax    = 10 * time.Second
	// bufferedReadWindow shortens the Pub/Sub read wait while events are
	// buffered, so the buffer retries even when no new message arrives.
	bufferedReadWindow = time.Second
	exitFlushTimeout   = 10 * time.Second
	// flushChunkEvents caps one insert transaction, so draining a long
	// backlog does not hold the write lock for long in turn.
	flushChunkEvents = 1000
)

// writeBuffer keeps normalized usage events the database could not accept yet.
// CPA delivers each event once: Pub/Sub pushes it to the subscriber only and
// drops a subscriber whose 256-message channel fills, and the pop transports
// have already removed it from the queue. A batch that failed on a locked
// database (index builds, ANALYZE, long maintenance transactions) was
// therefore lost; measured on production, a one-minute index build lost about
// 50 events. The consumers now keep reading while the buffer retries.
type writeBuffer struct {
	mu        sync.Mutex
	events    []usage.Event
	retryAt   time.Time
	backoff   time.Duration
	busySince time.Time
	dropped   int64
}

// writeEvents appends incoming events to the buffer and flushes it unless a
// recent busy failure asked it to wait. A busy database keeps the events and
// returns nil so the consumer keeps reading; a canceled context keeps them for
// the exit flush; any other failure drops them and is returned, as a failed
// batch was before.
func (m *Manager) writeEvents(ctx context.Context, cfg RuntimeConfig, incoming []usage.Event) error {
	b := &m.buffer
	b.mu.Lock()
	b.events = append(b.events, incoming...)
	if overflow := len(b.events) - maxBufferedEvents; overflow > 0 {
		if b.dropped == 0 {
			log.Printf("[collector] write buffer full, dropping oldest events limit=%d", maxBufferedEvents)
		}
		b.dropped += int64(overflow)
		b.events = append([]usage.Event(nil), b.events[overflow:]...)
	}
	if len(b.events) == 0 {
		b.mu.Unlock()
		return nil
	}
	if time.Now().Before(b.retryAt) {
		pending := len(b.events)
		b.mu.Unlock()
		m.setStatus(func(status *Status) {
			status.PendingEvents = pending
		})
		return nil
	}

	var inserted []usage.Event
	var flushed, skipped int
	for len(b.events) > 0 {
		chunk := b.events[:min(len(b.events), flushChunkEvents)]
		result, err := m.store.InsertEvents(ctx, chunk)
		if err != nil {
			switch {
			case ctx.Err() != nil:
			case codexinspectionrepo.IsSQLiteBusyError(err):
				now := time.Now()
				if b.busySince.IsZero() {
					b.busySince = now
					log.Printf("[collector] database busy, buffering usage events pending=%d: %v", len(b.events), err)
				}
				b.backoff = nextBufferRetry(b.backoff)
				b.retryAt = now.Add(b.backoff)
				err = nil
			default:
				b.events = nil
				b.resetBusy()
			}
			pending := len(b.events)
			b.mu.Unlock()
			m.afterInsert(ctx, cfg, inserted, skipped, pending)
			return err
		}
		flushed += len(chunk)
		inserted = append(inserted, insertedEvents(chunk, result.InsertedEventHashes)...)
		skipped += result.Skipped
		b.events = b.events[len(chunk):]
	}
	if !b.busySince.IsZero() {
		log.Printf("[collector] database writable again after %s, flushed usage events=%d dropped=%d",
			time.Since(b.busySince).Round(time.Second), flushed, b.dropped)
	}
	b.events = nil
	b.resetBusy()
	b.mu.Unlock()
	m.afterInsert(ctx, cfg, inserted, skipped, 0)
	return nil
}

// afterInsert runs the post-insert steps for the events a flush wrote and
// publishes the buffer size.
func (m *Manager) afterInsert(ctx context.Context, cfg RuntimeConfig, inserted []usage.Event, skipped, pending int) {
	if len(inserted) > 0 {
		if err := m.quotaSnapshots.WriteUsageEvents(ctx, inserted); err != nil {
			log.Printf("persist usage quota snapshots: %v", err)
		}
		m.handleUsageEvents(ctx, cfg, inserted)
	}
	m.setStatus(func(status *Status) {
		status.PendingEvents = pending
		if len(inserted) > 0 || skipped > 0 {
			status.LastInsertedAt = time.Now().UnixMilli()
			status.TotalInserted += int64(len(inserted))
			status.TotalSkipped += int64(skipped)
		}
	})
}

func (b *writeBuffer) resetBusy() {
	b.retryAt = time.Time{}
	b.backoff = 0
	b.busySince = time.Time{}
	b.dropped = 0
}

func (m *Manager) bufferedEvents() int {
	m.buffer.mu.Lock()
	defer m.buffer.mu.Unlock()
	return len(m.buffer.events)
}

// flushOnExit writes what the buffer still holds when a consumer stops,
// bounded so a stopping collector cannot hang on a locked database.
func (m *Manager) flushOnExit(cfg RuntimeConfig) {
	if m.bufferedEvents() == 0 {
		return
	}
	m.buffer.mu.Lock()
	m.buffer.retryAt = time.Time{}
	m.buffer.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), exitFlushTimeout)
	defer cancel()
	if err := m.writeEvents(ctx, cfg, nil); err != nil {
		log.Printf("[collector] flush buffered usage events on stop: %v", err)
	}
	if pending := m.bufferedEvents(); pending > 0 {
		log.Printf("[collector] stopped with buffered usage events unwritten=%d", pending)
	}
}

func nextBufferRetry(current time.Duration) time.Duration {
	if current < bufferRetryMin {
		return bufferRetryMin
	}
	if next := current * 2; next < bufferRetryMax {
		return next
	}
	return bufferRetryMax
}
