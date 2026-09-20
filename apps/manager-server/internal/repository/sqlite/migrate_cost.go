package sqlite

import (
	"database/sql"
	"fmt"
)

// usageEventCostRollupTables are the derived aggregates that carry stored
// per-event cost sums next to their token sums.
var usageEventCostRollupTables = []string{
	"usage_pricing_hourly_rollups_v1",
	"usage_pricing_account_rollups_v1",
	usageMonitoringAccountDailyTable,
	usageMonitoringAPIKeyDailyTable,
}

var usageEventCostRollupColumns = []struct {
	name       string
	definition string
}{
	{name: "cost_cny_nanos", definition: "integer not null default 0"},
	{name: "cost_usd_nanos", definition: "integer not null default 0"},
	{name: "unpriced_calls", definition: "integer not null default 0"},
}

// ensureUsageEventCostSchema creates the provider price book, the per-event
// cost table and its task state, and adds cost columns to the rollups that sum
// it. Existing rollup rows keep zero cost until a manual reprice rebuilds them,
// so this migration never forces a rebuild on upgrade.
func ensureUsageEventCostSchema(db *sql.DB) error {
	statements := []string{
		`create table if not exists provider_model_prices (
			id integer primary key autoincrement,
			provider text not null,
			model text not null,
			prompt_per_1m real not null,
			completion_per_1m real not null,
			cache_read_per_1m real not null default 0,
			cache_creation_per_1m real not null default 0,
			cache_read_configured integer not null default 0,
			cache_creation_configured integer not null default 0,
			timezone text not null default 'Asia/Shanghai',
			note text,
			off_days text not null default '',
			updated_at_ms integer not null,
			unique (provider, model)
		)`,
		`create table if not exists provider_model_price_windows (
			id integer primary key autoincrement,
			price_id integer not null references provider_model_prices(id) on delete cascade,
			start_minute integer not null,
			end_minute integer not null,
			multiplier real not null,
			label text,
			weekdays text not null default ''
		)`,
		`create index if not exists idx_provider_model_price_windows_price on provider_model_price_windows(price_id)`,
		`create table if not exists usage_event_costs_v1 (
			event_id integer primary key,
			cost_ts_ms integer not null,
			cost_provider text not null,
			cost_pricing_model text not null,
			cost_price_source text not null,
			cost_price_id integer,
			cost_window_id integer,
			cost_multiplier real not null default 1,
			cost_cny_nanos integer not null default 0,
			cost_usd_nanos integer not null default 0,
			cost_updated_at_ms integer not null
		)`,
		`create table if not exists usage_event_cost_state (
			rollup_name text primary key,
			status text not null,
			priced_through_event_id integer not null default 0,
			reprice_epoch integer not null default 0,
			reprice_from_ms integer not null default 0,
			reprice_target_event_id integer not null default 0,
			repriced_through_event_id integer not null default 0,
			processed_events integer not null default 0,
			last_run_started_at_ms integer,
			updated_at_ms integer not null default 0,
			finished_at_ms integer,
			last_error text
		)`,
		`insert or ignore into usage_event_cost_state (rollup_name, status, updated_at_ms)
			values ('event_cost', 'pending', 0)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("ensure usage event cost schema: %w", err)
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Weekday windows and off days arrived after the first deploy of the
	// provider price book; add them to databases created before that.
	for _, column := range []struct{ table, name, definition string }{
		{"provider_model_prices", "off_days", "text not null default ''"},
		{"provider_model_price_windows", "weekdays", "text not null default ''"},
	} {
		exists, err := tableHasColumn(tx, column.table, column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf(`alter table %s add column %s %s`, column.table, column.name, column.definition)); err != nil {
			return fmt.Errorf("add %s.%s: %w", column.table, column.name, err)
		}
	}
	for _, tableName := range usageEventCostRollupTables {
		for _, column := range usageEventCostRollupColumns {
			exists, err := tableHasColumn(tx, tableName, column.name)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			if _, err := tx.Exec(fmt.Sprintf(`alter table %s add column %s %s`, tableName, column.name, column.definition)); err != nil {
				return fmt.Errorf("add %s.%s: %w", tableName, column.name, err)
			}
		}
	}
	return tx.Commit()
}
