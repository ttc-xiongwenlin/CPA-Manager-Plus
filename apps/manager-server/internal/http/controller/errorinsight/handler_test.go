package errorinsight

import (
	"database/sql"
	"reflect"
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

func TestBuildInsightFilterMapsAllFields(t *testing.T) {
	req := insightRequest{
		FromMS:      1000,
		ToMS:        2000,
		SearchQuery: "timeout",
		Filters: insightFilters{
			Models:       []string{"gpt-4"},
			Providers:    []string{"codex"},
			Accounts:     []string{"a@b"},
			AuthFiles:    []string{"auth1.json"},
			AuthIndices:  []string{"0"},
			APIKeyHashes: []string{"hash1"},
			SourceHashes: []string{"srchash1"},
			MinLatencyMS: 500,
			BucketScope:  true,
		},
	}

	got := buildInsightFilter(req)

	if got.FromMS != req.FromMS {
		t.Errorf("FromMS = %d, want %d", got.FromMS, req.FromMS)
	}
	if got.ToMS != req.ToMS {
		t.Errorf("ToMS = %d, want %d", got.ToMS, req.ToMS)
	}
	if got.SearchQuery != req.SearchQuery {
		t.Errorf("SearchQuery = %q, want %q", got.SearchQuery, req.SearchQuery)
	}
	if !reflect.DeepEqual(got.Models, req.Filters.Models) {
		t.Errorf("Models = %#v, want %#v", got.Models, req.Filters.Models)
	}
	if !reflect.DeepEqual(got.Providers, req.Filters.Providers) {
		t.Errorf("Providers = %#v, want %#v", got.Providers, req.Filters.Providers)
	}
	if !reflect.DeepEqual(got.Accounts, req.Filters.Accounts) {
		t.Errorf("Accounts = %#v, want %#v", got.Accounts, req.Filters.Accounts)
	}
	if !reflect.DeepEqual(got.AuthFiles, req.Filters.AuthFiles) {
		t.Errorf("AuthFiles = %#v, want %#v", got.AuthFiles, req.Filters.AuthFiles)
	}
	if !reflect.DeepEqual(got.AuthIndices, req.Filters.AuthIndices) {
		t.Errorf("AuthIndices = %#v, want %#v", got.AuthIndices, req.Filters.AuthIndices)
	}
	if !reflect.DeepEqual(got.APIKeyHashes, req.Filters.APIKeyHashes) {
		t.Errorf("APIKeyHashes = %#v, want %#v", got.APIKeyHashes, req.Filters.APIKeyHashes)
	}
	if !reflect.DeepEqual(got.SourceHashes, req.Filters.SourceHashes) {
		t.Errorf("SourceHashes = %#v, want %#v", got.SourceHashes, req.Filters.SourceHashes)
	}
	if got.MinLatencyMS != req.Filters.MinLatencyMS {
		t.Errorf("MinLatencyMS = %d, want %d", got.MinLatencyMS, req.Filters.MinLatencyMS)
	}
	if got.BucketScope != req.Filters.BucketScope {
		t.Errorf("BucketScope = %v, want %v", got.BucketScope, req.Filters.BucketScope)
	}
	if got.IncludeFailed != false {
		t.Errorf("IncludeFailed = %v, want false (repo forces failed=1 semantics)", got.IncludeFailed)
	}
	if got.FailedOnly != false {
		t.Errorf("FailedOnly = %v, want false (repo forces failed=1 semantics)", got.FailedOnly)
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
