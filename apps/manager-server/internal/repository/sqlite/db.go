package sqlite

import (
	"database/sql"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	return OpenWithOptions(Options{Path: path})
}

func OpenWithOptions(options Options) (*sql.DB, error) {
	dbPath, err := filepath.Abs(options.Path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dataSourceName(dbPath))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(options.maxOpenConns())
	db.SetMaxIdleConns(options.maxIdleConns())
	db.SetConnMaxIdleTime(options.connMaxIdleTime())
	if err := Migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Keep sqlite_stat1 current so the planner can pick the covering indexes
	// without an `indexed by` pin. Without statistics the unpinned monitoring
	// reads fall back to wide-row scans (measured seconds per request on the
	// production database). `pragma optimize` self-limits analysis to sampling
	// and is a no-op when statistics are still fresh, so startup stays cheap.
	if _, err := db.Exec(`pragma optimize`); err != nil {
		log.Printf("sqlite: pragma optimize failed: %v", err)
	}
	return db, nil
}

func dataSourceName(path string) string {
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	dsn := &url.URL{
		Scheme: "file",
		Path:   uriPath,
	}
	query := dsn.Query()
	query.Add("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "synchronous(FULL)")
	dsn.RawQuery = query.Encode()
	return dsn.String()
}
