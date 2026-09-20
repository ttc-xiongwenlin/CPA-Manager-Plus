package usage

// CostTotals carries stored per-event cost sums through token aggregates.
// CNY nanos are real spend priced from provider rules; USD nanos are the
// default model-price book estimate for events without a provider rule. The
// two are never converted or merged. UnpricedCalls counts events no rule
// matched, including events the cost task has not priced yet.
type CostTotals struct {
	CostCNYNanos  int64
	CostUSDNanos  int64
	UnpricedCalls int64
}

const costNanosPerUnit = 1e9

// AddCost accumulates another total into the receiver.
func (c *CostTotals) AddCost(other CostTotals) {
	if c == nil {
		return
	}
	c.CostCNYNanos += other.CostCNYNanos
	c.CostUSDNanos += other.CostUSDNanos
	c.UnpricedCalls += other.UnpricedCalls
}

// CostCNY returns the CNY spend in yuan.
func (c CostTotals) CostCNY() float64 {
	return float64(c.CostCNYNanos) / costNanosPerUnit
}

// CostUSD returns the default-book USD estimate in dollars.
func (c CostTotals) CostUSD() float64 {
	return float64(c.CostUSDNanos) / costNanosPerUnit
}

// CostNanos converts a currency amount into integer nanos, rounding half away
// from zero so per-event rounding error stays below one nano.
func CostNanos(amount float64) int64 {
	if amount <= 0 {
		return 0
	}
	return int64(amount*costNanosPerUnit + 0.5)
}
