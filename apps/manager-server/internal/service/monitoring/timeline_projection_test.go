package monitoring

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

// The projected timeline aggregates in SQLite, so it must stay byte-identical
// to the raw usage_events reader it replaces, both before and after the
// projection has caught up with the events it covers.
func TestProjectedTimelineMatchesRawTimeline(t *testing.T) {
	db := newMonitoringTestStore(t)
	ctx := context.Background()
	service := New(db)

	if err := db.SaveModelPrices(ctx, map[string]store.ModelPrice{
		"gpt-a": {Prompt: 1, Completion: 2, Cache: 0.5},
	}); err != nil {
		t.Fatalf("save model prices: %v", err)
	}

	base := int64(1_778_000_000_000)
	base -= base % 3_600_000
	latency := int64(250)
	otherLatency := int64(80)
	events := make([]usage.Event, 0, 12)
	for hour := int64(0); hour < 4; hour++ {
		start := base + hour*3_600_000
		events = append(events,
			monitoringEvent(fmtHash("a", hour), start+1_000, "gpt-a", "auth-1", "source-a", false, 1_000_000, 500_000, 10, 100, 1_500_110, &latency),
			monitoringEvent(fmtHash("b", hour), start+2_000, "gpt-b", "auth-2", "source-b", true, 10, 20, 0, 0, 30, nil),
			monitoringEvent(fmtHash("c", hour), start+3_000, "gpt-a", "auth-1", "source-a", false, 5, 7, 1, 2, 15, &otherLatency),
		)
	}
	if _, err := db.InsertEvents(ctx, events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := store.AnalyticsFilter{
		FromMS:        base,
		ToMS:          base + 4*3_600_000,
		AuthIndices:   []string{"auth-1", "auth-2"},
		IncludeFailed: true,
	}

	compare := func(stage string) {
		t.Helper()
		for _, granularity := range []string{"hour", "day"} {
			want, err := db.TimelineWithFilter(ctx, filter, granularity, time.UTC)
			if err != nil {
				t.Fatalf("raw timeline: %v", err)
			}
			if len(want) == 0 {
				t.Fatalf("raw timeline returned no points (granularity=%s)", granularity)
			}
			got, available := service.monitoringReader.Timeline(ctx, filter, granularity, time.UTC)
			if !available {
				t.Fatalf("projected timeline unavailable (%s granularity=%s)", stage, granularity)
			}
			sortTimeline(want)
			sortTimeline(got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s granularity=%s\n got=%#v\nwant=%#v", stage, granularity, got, want)
			}
		}
	}

	// Events newer than the projection coverage are read from the raw tail.
	compare("raw tail")

	result, err := db.CatchUpUsageMonitoringProjection(ctx, 1000, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("catch up projection: %v", err)
	}
	if result.Processed != len(events) {
		t.Fatalf("projection processed %d events, want %d", result.Processed, len(events))
	}
	compare("projected")
}

// The API key timeline shares the projection path, including the raw reader's
// contract of returning nothing for an unscoped request.
func TestProjectedAPIKeyTimelineMatchesRawTimeline(t *testing.T) {
	db := newMonitoringTestStore(t)
	ctx := context.Background()
	service := New(db)

	if err := db.SaveModelPrices(ctx, map[string]store.ModelPrice{
		"gpt-a": {Prompt: 1, Completion: 2, Cache: 0.5},
	}); err != nil {
		t.Fatalf("save model prices: %v", err)
	}

	base := int64(1_778_000_000_000)
	base -= base % 3_600_000
	latency := int64(250)
	events := make([]usage.Event, 0, 8)
	for hour := int64(0); hour < 4; hour++ {
		start := base + hour*3_600_000
		first := monitoringEvent(fmtHash("ka", hour), start+1_000, "gpt-a", "auth-1", "source-a", false, 900, 400, 5, 30, 1_335, &latency)
		first.APIKeyHash = "key-1"
		second := monitoringEvent(fmtHash("kb", hour), start+2_000, "gpt-b", "auth-2", "source-b", true, 10, 20, 0, 0, 30, nil)
		second.APIKeyHash = "key-2"
		events = append(events, first, second)
	}
	if _, err := db.InsertEvents(ctx, events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := store.AnalyticsFilter{
		FromMS:        base,
		ToMS:          base + 4*3_600_000,
		APIKeyHashes:  []string{"key-1", "key-2"},
		IncludeFailed: true,
	}

	compare := func(stage string) {
		t.Helper()
		for _, granularity := range []string{"hour", "day"} {
			want, err := db.APIKeyTimelineWithFilter(ctx, filter, granularity, time.UTC)
			if err != nil {
				t.Fatalf("raw api key timeline: %v", err)
			}
			if len(want) == 0 {
				t.Fatalf("raw api key timeline returned no points (granularity=%s)", granularity)
			}
			got, available := service.monitoringReader.APIKeyTimeline(ctx, filter, granularity, time.UTC)
			if !available {
				t.Fatalf("projected api key timeline unavailable (%s granularity=%s)", stage, granularity)
			}
			// The builders preserve reader order, so it is part of the contract.
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s granularity=%s\n got=%#v\nwant=%#v", stage, granularity, got, want)
			}
		}
	}

	compare("raw tail")
	if _, err := db.CatchUpUsageMonitoringProjection(ctx, 1000, time.Now().UnixMilli()); err != nil {
		t.Fatalf("catch up projection: %v", err)
	}
	compare("projected")

	unscoped := filter
	unscoped.APIKeyHashes = nil
	if _, available := service.monitoringReader.APIKeyTimeline(ctx, unscoped, "hour", time.UTC); available {
		t.Fatal("unscoped api key timeline unexpectedly served from the projection")
	}
}

// The credential timeline carries identity labels through the projection, so
// it has to reproduce the raw reader's grouping exactly.
func TestProjectedCredentialTimelineMatchesRawTimeline(t *testing.T) {
	db := newMonitoringTestStore(t)
	ctx := context.Background()
	service := New(db)

	if err := db.SaveModelPrices(ctx, map[string]store.ModelPrice{
		"gpt-a": {Prompt: 1, Completion: 2, Cache: 0.5},
	}); err != nil {
		t.Fatalf("save model prices: %v", err)
	}

	base := int64(1_778_000_000_000)
	base -= base % 3_600_000
	latency := int64(250)
	events := make([]usage.Event, 0, 8)
	for hour := int64(0); hour < 4; hour++ {
		start := base + hour*3_600_000
		first := monitoringEvent(fmtHash("ca", hour), start+1_000, "gpt-a", "auth-1", "source-a", false, 900, 400, 5, 30, 1_335, &latency)
		first.AuthFileSnapshot = "alice.json"
		first.AccountSnapshot = "alice@example.com"
		first.AuthLabelSnapshot = "Alice"
		first.AuthProviderSnapshot = "codex"
		first.AuthProjectIDSnapshot = "project-a"
		second := monitoringEvent(fmtHash("cb", hour), start+2_000, "gpt-b", "auth-2", "source-b", true, 10, 20, 0, 0, 30, nil)
		second.AuthFileSnapshot = "bob.json"
		second.AccountSnapshot = "bob@example.com"
		second.AuthLabelSnapshot = "Bob"
		second.AuthProviderSnapshot = "claude"
		events = append(events, first, second)
	}
	if _, err := db.InsertEvents(ctx, events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	filter := store.AnalyticsFilter{
		FromMS:        base,
		ToMS:          base + 4*3_600_000,
		CredentialIDs: []string{"alice.json", "bob.json"},
		IncludeFailed: true,
	}

	compare := func(stage string) {
		t.Helper()
		for _, granularity := range []string{"hour", "day"} {
			want, err := db.CredentialTimelineWithFilter(ctx, filter, granularity, time.UTC)
			if err != nil {
				t.Fatalf("raw credential timeline: %v", err)
			}
			if len(want) == 0 {
				t.Fatalf("raw credential timeline returned no points (granularity=%s)", granularity)
			}
			got, available := service.monitoringReader.CredentialTimeline(ctx, filter, granularity, time.UTC)
			if !available {
				t.Fatalf("projected credential timeline unavailable (%s granularity=%s)", stage, granularity)
			}
			// The builders preserve reader order, so it is part of the contract.
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s granularity=%s\n got=%#v\nwant=%#v", stage, granularity, got, want)
			}
		}
	}

	compare("raw tail")
	if _, err := db.CatchUpUsageMonitoringProjection(ctx, 1000, time.Now().UnixMilli()); err != nil {
		t.Fatalf("catch up projection: %v", err)
	}
	compare("projected")
}

// A local hour bucket that splits a UTC hour cannot be derived from hourly
// rows, so the reader must decline instead of reporting shifted buckets.
func TestProjectedTimelineDeclinesSubHourOffsetZones(t *testing.T) {
	db := newMonitoringTestStore(t)
	service := New(db)
	location, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Skipf("load location: %v", err)
	}
	filter := store.AnalyticsFilter{
		FromMS:        1_778_000_000_000,
		ToMS:          1_778_000_000_000 + 4*3_600_000,
		IncludeFailed: true,
	}
	if _, available := service.monitoringReader.Timeline(context.Background(), filter, "hour", location); available {
		t.Fatal("sub-hour offset zone unexpectedly served from hourly rows")
	}
}

func fmtHash(prefix string, hour int64) string {
	return prefix + "-" + time.UnixMilli(hour).UTC().Format("150405.000")
}

func sortTimeline(points []store.TimelinePoint) {
	slices.SortFunc(points, func(left, right store.TimelinePoint) int {
		if left.BucketMS != right.BucketMS {
			if left.BucketMS < right.BucketMS {
				return -1
			}
			return 1
		}
		switch {
		case left.Model < right.Model:
			return -1
		case left.Model > right.Model:
			return 1
		}
		switch {
		case left.BillingModel < right.BillingModel:
			return -1
		case left.BillingModel > right.BillingModel:
			return 1
		}
		return 0
	})
}
