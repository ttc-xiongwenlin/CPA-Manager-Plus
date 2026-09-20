package providerprice

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	providerpricesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerprice"
)

const (
	PricesPath   = "/v0/management/provider-prices"
	ObservedPath = "/v0/management/provider-prices/observed"
	RepricePath  = "/v0/management/pricing/reprice"
)

type Handler struct {
	App *app.Context
}

// Handles reports whether the path belongs to this controller.
func Handles(path string) bool {
	clean := strings.TrimRight(path, "/")
	return clean == PricesPath || clean == ObservedPath || clean == RepricePath
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}
	path := strings.TrimRight(r.URL.Path, "/")
	switch {
	case path == PricesPath && r.Method == http.MethodGet:
		prices, err := h.App.ProviderPriceService.List(r.Context())
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"prices": prices})
	case path == PricesPath && r.Method == http.MethodPut:
		var req providerpricesvc.UpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		prices, err := h.App.ProviderPriceService.Replace(r.Context(), req.Prices)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, providerpricesvc.ErrInvalidPrices) {
				status = http.StatusBadRequest
			}
			response.Error(w, status, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"prices": prices})
	case path == ObservedPath && r.Method == http.MethodGet:
		items, err := h.App.ProviderPriceService.Observed(r.Context(), time.Now().UnixMilli())
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"items": items})
	case path == RepricePath && r.Method == http.MethodGet:
		state, err := h.App.ProviderPriceService.RepriceState(r.Context())
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, state)
	case path == RepricePath && r.Method == http.MethodPost:
		var req providerpricesvc.RepriceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		if req.FromMS < 0 {
			response.Error(w, http.StatusBadRequest, errors.New("fromMs must not be negative"))
			return
		}
		state, err := h.App.ProviderPriceService.StartReprice(r.Context(), req.FromMS)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, state)
	default:
		response.MethodNotAllowed(w)
	}
}
