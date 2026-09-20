package model

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usageidentity"
)

const (
	// ProviderPriceMinutesPerDay bounds window minutes measured from local midnight.
	ProviderPriceMinutesPerDay = 24 * 60
	// DefaultProviderPriceTimezone applies when a provider price has no timezone.
	DefaultProviderPriceTimezone = "Asia/Shanghai"
)

// ProviderPriceWindow scales a provider price during one daily local-time
// window. Minutes count from local midnight; EndMinute <= StartMinute wraps
// past midnight, so 990..30 covers 16:30 through 00:30 the next day. Weekdays
// are ISO days (1 = Monday … 7 = Sunday) the window applies on; empty means
// every day. A wrapped window is attributed to the day it starts on.
type ProviderPriceWindow struct {
	ID          int64   `json:"id,omitempty"`
	StartMinute int     `json:"startMinute"`
	EndMinute   int     `json:"endMinute"`
	Multiplier  float64 `json:"multiplier"`
	Label       string  `json:"label,omitempty"`
	Weekdays    []int   `json:"weekdays,omitempty"`
}

// ProviderModelPrice is the real CNY price one provider charges for one model.
// Rates are CNY per 1M tokens. Unconfigured cache rates fall back to the
// prompt rate, so an unknown vendor is never assumed to discount cache hits.
type ProviderModelPrice struct {
	ID                      int64                 `json:"id,omitempty"`
	Provider                string                `json:"provider"`
	Model                   string                `json:"model"`
	Prompt                  float64               `json:"prompt"`
	Completion              float64               `json:"completion"`
	CacheRead               float64               `json:"cacheRead,omitempty"`
	CacheCreation           float64               `json:"cacheCreation,omitempty"`
	CacheReadConfigured     bool                  `json:"cacheReadConfigured,omitempty"`
	CacheCreationConfigured bool                  `json:"cacheCreationConfigured,omitempty"`
	Timezone                string                `json:"timezone"`
	Note                    string                `json:"note,omitempty"`
	Windows                 []ProviderPriceWindow `json:"windows,omitempty"`
	// OffDays are local dates (YYYY-MM-DD) on which no window applies, so the
	// whole day charges the base rate. Providers use it for public holidays.
	OffDays     []string `json:"offDays,omitempty"`
	UpdatedAtMS int64    `json:"updatedAtMs,omitempty"`
}

const providerPriceOffDayLayout = "2006-01-02"

// NormalizeProviderModelPrice trims identifiers, normalizes the provider key,
// sorts windows, and validates every rate and window. The input is not mutated.
func NormalizeProviderModelPrice(price ProviderModelPrice) (ProviderModelPrice, error) {
	normalized := price
	normalized.Provider = usageidentity.ProviderKey(price.Provider, "")
	normalized.Model = strings.TrimSpace(price.Model)
	normalized.Timezone = strings.TrimSpace(price.Timezone)
	normalized.Note = strings.TrimSpace(price.Note)
	if normalized.Provider == "" {
		return ProviderModelPrice{}, errors.New("provider is required")
	}
	if normalized.Model == "" {
		return ProviderModelPrice{}, fmt.Errorf("model is required for provider %s", normalized.Provider)
	}
	if normalized.Timezone == "" {
		normalized.Timezone = DefaultProviderPriceTimezone
	}
	if _, err := time.LoadLocation(normalized.Timezone); err != nil {
		return ProviderModelPrice{}, fmt.Errorf("invalid timezone %q for %s/%s", normalized.Timezone, normalized.Provider, normalized.Model)
	}
	if !validModelPriceRuleValue(normalized.Prompt) ||
		!validModelPriceRuleValue(normalized.Completion) ||
		!validModelPriceRuleValue(normalized.CacheRead) ||
		!validModelPriceRuleValue(normalized.CacheCreation) {
		return ProviderModelPrice{}, fmt.Errorf("invalid price for %s/%s", normalized.Provider, normalized.Model)
	}
	if !normalized.CacheReadConfigured {
		normalized.CacheRead = 0
	}
	if !normalized.CacheCreationConfigured {
		normalized.CacheCreation = 0
	}
	windows, err := normalizeProviderPriceWindows(normalized.Windows)
	if err != nil {
		return ProviderModelPrice{}, fmt.Errorf("invalid window for %s/%s: %w", normalized.Provider, normalized.Model, err)
	}
	normalized.Windows = windows
	offDays, err := normalizeProviderPriceOffDays(normalized.OffDays)
	if err != nil {
		return ProviderModelPrice{}, fmt.Errorf("invalid off day for %s/%s: %w", normalized.Provider, normalized.Model, err)
	}
	normalized.OffDays = offDays
	return normalized, nil
}

func normalizeProviderPriceOffDays(days []string) ([]string, error) {
	if len(days) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(days))
	for _, day := range days {
		day = strings.TrimSpace(day)
		if day == "" {
			continue
		}
		if _, err := time.Parse(providerPriceOffDayLayout, day); err != nil {
			return nil, fmt.Errorf("date %q must be YYYY-MM-DD", day)
		}
		if seen[day] {
			continue
		}
		seen[day] = true
		normalized = append(normalized, day)
	}
	sort.Strings(normalized)
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func normalizeProviderPriceWeekdays(weekdays []int) ([]int, error) {
	if len(weekdays) == 0 {
		return nil, nil
	}
	seen := map[int]bool{}
	normalized := make([]int, 0, len(weekdays))
	for _, weekday := range weekdays {
		if weekday < 1 || weekday > 7 {
			return nil, fmt.Errorf("weekday %d out of range 1..7", weekday)
		}
		if seen[weekday] {
			continue
		}
		seen[weekday] = true
		normalized = append(normalized, weekday)
	}
	sort.Ints(normalized)
	if len(normalized) == 7 {
		return nil, nil
	}
	return normalized, nil
}

func normalizeProviderPriceWindows(windows []ProviderPriceWindow) ([]ProviderPriceWindow, error) {
	if len(windows) == 0 {
		return nil, nil
	}
	normalized := make([]ProviderPriceWindow, 0, len(windows))
	for _, window := range windows {
		window.Label = strings.TrimSpace(window.Label)
		if window.StartMinute < 0 || window.StartMinute >= ProviderPriceMinutesPerDay {
			return nil, fmt.Errorf("start minute %d out of range", window.StartMinute)
		}
		if window.EndMinute < 0 || window.EndMinute > ProviderPriceMinutesPerDay {
			return nil, fmt.Errorf("end minute %d out of range", window.EndMinute)
		}
		if window.EndMinute == ProviderPriceMinutesPerDay {
			window.EndMinute = 0
		}
		if window.StartMinute == window.EndMinute {
			return nil, fmt.Errorf("window %d..%d is empty", window.StartMinute, window.EndMinute)
		}
		if !(window.Multiplier > 0) || math.IsInf(window.Multiplier, 0) {
			return nil, fmt.Errorf("multiplier %v must be positive", window.Multiplier)
		}
		weekdays, err := normalizeProviderPriceWeekdays(window.Weekdays)
		if err != nil {
			return nil, err
		}
		window.Weekdays = weekdays
		normalized = append(normalized, window)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].StartMinute < normalized[j].StartMinute
	})
	for i := range normalized {
		for j := i + 1; j < len(normalized); j++ {
			if providerPriceWindowsOverlap(normalized[i], normalized[j]) {
				return nil, fmt.Errorf("windows %d..%d and %d..%d overlap",
					normalized[i].StartMinute, normalized[i].EndMinute,
					normalized[j].StartMinute, normalized[j].EndMinute)
			}
		}
	}
	return normalized, nil
}

func providerPriceWindowsOverlap(left, right ProviderPriceWindow) bool {
	if !weekdaysIntersect(left.Weekdays, right.Weekdays) {
		return false
	}
	for _, a := range left.intervals() {
		for _, b := range right.intervals() {
			if a[0] < b[1] && b[0] < a[1] {
				return true
			}
		}
	}
	return false
}

// intervals expands a window into half-open [start, end) ranges on a single
// day, splitting wrapped windows at midnight.
func (w ProviderPriceWindow) intervals() [][2]int {
	if w.StartMinute < w.EndMinute {
		return [][2]int{{w.StartMinute, w.EndMinute}}
	}
	return [][2]int{{w.StartMinute, ProviderPriceMinutesPerDay}, {0, w.EndMinute}}
}

func weekdaysIntersect(left, right []int) bool {
	if len(left) == 0 || len(right) == 0 {
		return true
	}
	for _, a := range left {
		for _, b := range right {
			if a == b {
				return true
			}
		}
	}
	return false
}

// Contains reports whether a local minute-of-day falls inside the window.
func (w ProviderPriceWindow) Contains(minute int) bool {
	if w.StartMinute < w.EndMinute {
		return minute >= w.StartMinute && minute < w.EndMinute
	}
	return minute >= w.StartMinute || minute < w.EndMinute
}

// AppliesOn reports whether the window is active on an ISO weekday (1 = Monday).
func (w ProviderPriceWindow) AppliesOn(isoWeekday int) bool {
	if len(w.Weekdays) == 0 {
		return true
	}
	for _, weekday := range w.Weekdays {
		if weekday == isoWeekday {
			return true
		}
	}
	return false
}

// MatchWindow returns the window covering a local minute-of-day on any day.
func (p ProviderModelPrice) MatchWindow(minute int) (ProviderPriceWindow, bool) {
	for _, window := range p.Windows {
		if window.Contains(minute) {
			return window, true
		}
	}
	return ProviderPriceWindow{}, false
}

// MatchWindowAt returns the window covering a local time, honoring weekday
// restrictions and off days. A wrapped window that started the previous day
// still matches minutes before its end on the following day.
func (p ProviderModelPrice) MatchWindowAt(local time.Time) (ProviderPriceWindow, bool) {
	if p.IsOffDay(local) {
		return ProviderPriceWindow{}, false
	}
	minute := local.Hour()*60 + local.Minute()
	weekday := ISOWeekday(local.Weekday())
	previous := ISOWeekday(local.AddDate(0, 0, -1).Weekday())
	for _, window := range p.Windows {
		if !window.Contains(minute) {
			continue
		}
		startedToday := window.StartMinute < window.EndMinute || minute >= window.StartMinute
		if startedToday && window.AppliesOn(weekday) {
			return window, true
		}
		if !startedToday && window.AppliesOn(previous) && !p.IsOffDay(local.AddDate(0, 0, -1)) {
			return window, true
		}
	}
	return ProviderPriceWindow{}, false
}

// IsOffDay reports whether the local date is listed as an off day.
func (p ProviderModelPrice) IsOffDay(local time.Time) bool {
	if len(p.OffDays) == 0 {
		return false
	}
	day := local.Format(providerPriceOffDayLayout)
	for _, offDay := range p.OffDays {
		if offDay == day {
			return true
		}
	}
	return false
}

// ISOWeekday maps Go's Sunday-first weekday onto ISO numbering (1 = Monday).
func ISOWeekday(weekday time.Weekday) int {
	if weekday == time.Sunday {
		return 7
	}
	return int(weekday)
}

// LocalMinute converts an event timestamp into minutes since local midnight in
// the supplied location.
func LocalMinute(timestampMS int64, location *time.Location) int {
	if location == nil {
		location = time.UTC
	}
	local := time.UnixMilli(timestampMS).In(location)
	return local.Hour()*60 + local.Minute()
}

// FlatPrice maps the provider rates onto the five-rate ModelPrice shape used by
// the pricing package. Unconfigured cache rates charge the prompt rate and the
// legacy cache rate mirrors cache reads so compatible cached tokens price the
// same way as fine-grained cache reads.
func (p ProviderModelPrice) FlatPrice() ModelPrice {
	cacheRead := p.Prompt
	if p.CacheReadConfigured {
		cacheRead = p.CacheRead
	}
	cacheCreation := p.Prompt
	if p.CacheCreationConfigured {
		cacheCreation = p.CacheCreation
	}
	return ModelPrice{
		Prompt:                  p.Prompt,
		Completion:              p.Completion,
		Cache:                   cacheRead,
		CacheRead:               cacheRead,
		CacheCreation:           cacheCreation,
		PromptConfigured:        true,
		CompletionConfigured:    true,
		CacheReadConfigured:     true,
		CacheCreationConfigured: true,
	}
}
