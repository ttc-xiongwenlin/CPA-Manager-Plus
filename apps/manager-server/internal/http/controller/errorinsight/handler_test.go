package errorinsight

import (
	"database/sql"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestValidateWindow(t *testing.T) {
	const maxMS = 14 * 24 * 60 * 60 * 1000
	cases := []struct {
		name    string
		fromMS  int64
		toMS    int64
		wantErr bool
	}{
		{"valid one hour", 1000, 1000 + 3600_000, false},
		{"valid exactly 14d", 1000, 1000 + maxMS, false},
		{"over 14d", 1000, 1000 + maxMS + 1, true},
		{"reversed", 2000, 1000, true},
		{"zero from", 0, 1000, true},
		{"equal", 1000, 1000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateWindow(c.fromMS, c.toMS)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateWindow(%d, %d) err = %v, wantErr %v", c.fromMS, c.toMS, err, c.wantErr)
			}
		})
	}
}

func TestBuildResponseMapsNullableFields(t *testing.T) {
	got := buildResponse(
		[]store.ErrorClassStat{{Class: "auth", Count: 3}},
		[]store.ErrorClassTimelinePoint{{BucketMS: 7200000, Class: "auth", Count: 3}},
		[]store.ErrorClassRecentFailure{{
			Class:       "auth",
			TimestampMS: 7300000,
			StatusCode:  sql.NullInt64{Int64: 401, Valid: true},
			Model:       "gpt-test",
			Account:     "a@b",
			Provider:    "codex",
			Summary:     "unauthorized",
			LatencyMS:   sql.NullInt64{},
		}},
	)
	if len(got.Classes) != 1 || got.Classes[0].Class != "auth" || got.Classes[0].Count != 3 {
		t.Fatalf("classes = %#v", got.Classes)
	}
	if len(got.Timeline) != 1 || got.Timeline[0].BucketMS != 7200000 {
		t.Fatalf("timeline = %#v", got.Timeline)
	}
	if len(got.Recent) != 1 {
		t.Fatalf("recent = %#v", got.Recent)
	}
	if got.Recent[0].StatusCode != 401 {
		t.Errorf("status_code = %d, want 401", got.Recent[0].StatusCode)
	}
	if got.Recent[0].LatencyMS != 0 {
		t.Errorf("latency_ms = %d, want 0 for null latency", got.Recent[0].LatencyMS)
	}
}
