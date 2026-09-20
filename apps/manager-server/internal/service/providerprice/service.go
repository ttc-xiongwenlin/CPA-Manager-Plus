// Package providerprice manages the real per-provider CNY price rules and the
// manual reprice of stored event cost.
package providerprice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// ErrInvalidPrices marks a request the client must fix.
var ErrInvalidPrices = errors.New("invalid provider prices")

const observedWindow = 30 * 24 * time.Hour

type Service struct {
	store    *store.Store
	notifier func()
}

func New(store *store.Store) *Service {
	return &Service{store: store}
}

// SetChangedNotifier wakes the derived worker after price edits and reprices
// so new events pick the new book up immediately.
func (s *Service) SetChangedNotifier(notifier func()) {
	s.notifier = notifier
}

func (s *Service) notify() {
	if s.notifier != nil {
		s.notifier()
	}
}

type UpdateRequest struct {
	Prices []model.ProviderModelPrice `json:"prices"`
}

type RepriceRequest struct {
	FromMS int64 `json:"fromMs"`
}

type ObservedProviderModel struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Calls      int64  `json:"calls"`
	LastSeenMS int64  `json:"lastSeenMs"`
}

// RepriceState is the event cost task state as the panel shows it.
type RepriceState struct {
	Status                 string `json:"status"`
	PricedThroughEventID   int64  `json:"pricedThroughEventId"`
	LatestEventID          int64  `json:"latestEventId"`
	Repricing              bool   `json:"repricing"`
	RepriceEpoch           int64  `json:"repriceEpoch"`
	RepriceFromMS          int64  `json:"repriceFromMs"`
	RepriceTargetEventID   int64  `json:"repriceTargetEventId"`
	RepricedThroughEventID int64  `json:"repricedThroughEventId"`
	ProcessedEvents        int64  `json:"processedEvents"`
	UpdatedAtMS            int64  `json:"updatedAtMs"`
	LastError              string `json:"lastError,omitempty"`
}

func (s *Service) List(ctx context.Context) ([]model.ProviderModelPrice, error) {
	prices, err := s.store.LoadProviderPrices(ctx)
	if err != nil {
		return nil, err
	}
	if prices == nil {
		prices = []model.ProviderModelPrice{}
	}
	return prices, nil
}

// Replace validates and stores the whole rule set. Stored event cost is not
// touched: new events use the new rules, history changes only via Reprice.
func (s *Service) Replace(ctx context.Context, prices []model.ProviderModelPrice) ([]model.ProviderModelPrice, error) {
	if prices == nil {
		prices = []model.ProviderModelPrice{}
	}
	for _, price := range prices {
		if _, err := model.NormalizeProviderModelPrice(price); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPrices, err)
		}
	}
	saved, err := s.store.SaveProviderPrices(ctx, prices)
	if err != nil {
		if errors.Is(err, ErrInvalidPrices) {
			return nil, err
		}
		// Duplicate provider/model pairs are caught by the repository.
		return nil, fmt.Errorf("%w: %v", ErrInvalidPrices, err)
	}
	if saved == nil {
		saved = []model.ProviderModelPrice{}
	}
	s.notify()
	return saved, nil
}

func (s *Service) Observed(ctx context.Context, nowMS int64) ([]ObservedProviderModel, error) {
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	items, err := s.store.ObservedProviderModels(ctx, nowMS-observedWindow.Milliseconds())
	if err != nil {
		return nil, err
	}
	result := make([]ObservedProviderModel, 0, len(items))
	for _, item := range items {
		result = append(result, ObservedProviderModel{
			Provider:   item.Provider,
			Model:      item.Model,
			Calls:      item.Calls,
			LastSeenMS: item.LastSeenMS,
		})
	}
	return result, nil
}

func (s *Service) RepriceState(ctx context.Context) (RepriceState, error) {
	state, err := s.store.UsageEventCostState(ctx)
	if err != nil {
		return RepriceState{}, err
	}
	return repriceStateFrom(state), nil
}

// StartReprice prices every event at or after fromMS again with the current
// rules and rebuilds the rollups that sum stored cost.
func (s *Service) StartReprice(ctx context.Context, fromMS int64) (RepriceState, error) {
	state, err := s.store.StartUsageEventCostReprice(ctx, fromMS, time.Now().UnixMilli())
	if err != nil {
		return RepriceState{}, err
	}
	s.notify()
	return repriceStateFrom(state), nil
}

func repriceStateFrom(state store.UsageEventCostState) RepriceState {
	return RepriceState{
		Status:                 state.Status,
		PricedThroughEventID:   state.PricedThroughEventID,
		LatestEventID:          state.LatestEventID,
		Repricing:              state.Repricing(),
		RepriceEpoch:           state.RepriceEpoch,
		RepriceFromMS:          state.RepriceFromMS,
		RepriceTargetEventID:   state.RepriceTargetEventID,
		RepricedThroughEventID: state.RepricedThroughEventID,
		ProcessedEvents:        state.ProcessedEvents,
		UpdatedAtMS:            state.UpdatedAtMS,
		LastError:              state.LastError,
	}
}
