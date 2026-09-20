package providerprice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
	providerpricesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerprice"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func newHandler(t *testing.T) (*Handler, func(method, path, body string) *httptest.ResponseRecorder) {
	t.Helper()
	cfg := testutil.NewConfig(t)
	st := testutil.NewStore(t, cfg)
	ctx := context.Background()
	if _, err := st.UsageEvents.InsertBatch(ctx, []usage.Event{{
		EventHash: "deepseek-1", TimestampMS: 1_800_000_000_000, Timestamp: "2027-01-15T00:00:00Z",
		Provider: "openai-compatible-deepseek", Model: "deepseek-flash", ResolvedModel: "deepseek-flash",
		InputTokens: 1000, OutputTokens: 10, TotalTokens: 1010, CreatedAtMS: 1_800_000_000_000,
	}}); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	if _, err := st.CatchUpUsageEventCost(ctx, 10, 1_800_000_001_000); err != nil {
		t.Fatalf("cost catch-up: %v", err)
	}
	handler := &Handler{App: &app.Context{
		Config:               cfg,
		AdminAuthService:     adminauthsvc.New(cfg, st),
		ProviderPriceService: providerpricesvc.New(st),
	}}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		recorder := httptest.NewRecorder()
		handler.Handle(recorder, req)
		return recorder
	}
	return handler, call
}

func TestHandleRequiresPanelAuthorization(t *testing.T) {
	handler, _ := newHandler(t)
	recorder := httptest.NewRecorder()
	handler.Handle(recorder, httptest.NewRequest(http.MethodGet, PricesPath, nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !Handles(PricesPath) || !Handles(ObservedPath+"/") || !Handles(RepricePath) || Handles("/v0/management/model-prices") {
		t.Fatal("Handles must match exactly the three provider price paths")
	}
}

func TestHandleReplacesListsAndValidatesRules(t *testing.T) {
	_, call := newHandler(t)
	saved := call(http.MethodPut, PricesPath, `{"prices":[{"provider":"OpenAI_Compatible-DeepSeek","model":"deepseek-flash","prompt":2,"completion":8,"cacheRead":0.2,"cacheReadConfigured":true,"windows":[{"startMinute":30,"endMinute":510,"multiplier":0.5,"label":"off-peak"}]}]}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("put status = %d body = %s", saved.Code, saved.Body.String())
	}
	var payload struct {
		Prices []model.ProviderModelPrice `json:"prices"`
	}
	if err := json.NewDecoder(saved.Body).Decode(&payload); err != nil {
		t.Fatalf("decode put: %v", err)
	}
	if len(payload.Prices) != 1 || payload.Prices[0].Provider != "openai-compatible-deepseek" || payload.Prices[0].ID == 0 ||
		len(payload.Prices[0].Windows) != 1 || payload.Prices[0].Windows[0].Label != "off-peak" || payload.Prices[0].Timezone != model.DefaultProviderPriceTimezone {
		t.Fatalf("saved prices = %#v", payload.Prices)
	}

	listed := call(http.MethodGet, PricesPath, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"provider":"openai-compatible-deepseek"`) {
		t.Fatalf("get status = %d body = %s", listed.Code, listed.Body.String())
	}

	invalid := call(http.MethodPut, PricesPath, `{"prices":[{"provider":"p","model":"m","prompt":1,"completion":1,"windows":[{"startMinute":0,"endMinute":120,"multiplier":1},{"startMinute":60,"endMinute":180,"multiplier":1}]}]}`)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "overlap") {
		t.Fatalf("invalid status = %d body = %s", invalid.Code, invalid.Body.String())
	}
	duplicate := call(http.MethodPut, PricesPath, `{"prices":[{"provider":"p","model":"m","prompt":1,"completion":1},{"provider":"P","model":"m","prompt":2,"completion":1}]}`)
	if duplicate.Code != http.StatusBadRequest || !strings.Contains(duplicate.Body.String(), "duplicate") {
		t.Fatalf("duplicate status = %d body = %s", duplicate.Code, duplicate.Body.String())
	}
	// Failed writes must not disturb the stored rules.
	listed = call(http.MethodGet, PricesPath, "")
	if !strings.Contains(listed.Body.String(), `"deepseek-flash"`) {
		t.Fatalf("rules lost after rejected put: %s", listed.Body.String())
	}

	cleared := call(http.MethodPut, PricesPath, `{"prices":[]}`)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"prices":[]`) {
		t.Fatalf("clear status = %d body = %s", cleared.Code, cleared.Body.String())
	}
}

func TestHandleObservedAndReprice(t *testing.T) {
	_, call := newHandler(t)
	observed := call(http.MethodGet, ObservedPath, "")
	if observed.Code != http.StatusOK {
		t.Fatalf("observed status = %d body = %s", observed.Code, observed.Body.String())
	}
	var observedPayload struct {
		Items []providerpricesvc.ObservedProviderModel `json:"items"`
	}
	if err := json.NewDecoder(observed.Body).Decode(&observedPayload); err != nil {
		t.Fatalf("decode observed: %v", err)
	}
	if len(observedPayload.Items) != 1 || observedPayload.Items[0].Provider != "openai-compatible-deepseek" ||
		observedPayload.Items[0].Model != "deepseek-flash" || observedPayload.Items[0].Calls != 1 || observedPayload.Items[0].LastSeenMS != 1_800_000_000_000 {
		t.Fatalf("observed items = %#v", observedPayload.Items)
	}

	state := call(http.MethodGet, RepricePath, "")
	if state.Code != http.StatusOK {
		t.Fatalf("reprice state status = %d body = %s", state.Code, state.Body.String())
	}
	var statePayload providerpricesvc.RepriceState
	if err := json.NewDecoder(state.Body).Decode(&statePayload); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if statePayload.Status != "ready" || statePayload.PricedThroughEventID != 1 || statePayload.LatestEventID != 1 || statePayload.Repricing {
		t.Fatalf("state = %#v", statePayload)
	}

	negative := call(http.MethodPost, RepricePath, `{"fromMs":-1}`)
	if negative.Code != http.StatusBadRequest {
		t.Fatalf("negative from status = %d", negative.Code)
	}
	started := call(http.MethodPost, RepricePath, `{"fromMs":0}`)
	if started.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", started.Code, started.Body.String())
	}
	if err := json.NewDecoder(started.Body).Decode(&statePayload); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if !statePayload.Repricing || statePayload.RepriceEpoch != 1 || statePayload.RepriceTargetEventID != 1 || statePayload.Status != "repricing" {
		t.Fatalf("started state = %#v", statePayload)
	}
	if empty := call(http.MethodPost, RepricePath, ""); empty.Code != http.StatusOK {
		t.Fatalf("empty body should default to all history: %d %s", empty.Code, empty.Body.String())
	}
	if other := call(http.MethodDelete, RepricePath, ""); other.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete status = %d", other.Code)
	}
}
