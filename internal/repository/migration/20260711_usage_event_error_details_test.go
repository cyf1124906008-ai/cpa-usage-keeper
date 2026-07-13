package migration

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAddUsageEventErrorDetailsMigrationAddsDefaultedColumns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(testSQLiteDSN(filepath.Join(t.TempDir(), "legacy.db"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer closeOpenedDatabase(t, db)

	if err := db.Exec(`CREATE TABLE usage_events (
		id integer PRIMARY KEY,
		event_key text,
		failed numeric,
		timestamp datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy usage_events table: %v", err)
	}
	if err := db.Exec(`INSERT INTO usage_events (id, event_key, failed, timestamp)
		VALUES (?, ?, ?, ?)`, int64(1), "event-1", true, "2026-07-11 00:00:00").Error; err != nil {
		t.Fatalf("seed legacy usage event: %v", err)
	}

	if err := addUsageEventErrorDetailsMigration(db); err != nil {
		t.Fatalf("add usage event error details: %v", err)
	}
	if err := addUsageEventErrorDetailsMigration(db); err != nil {
		t.Fatalf("migration should be idempotent: %v", err)
	}

	for _, column := range []string{"status_code", "error_type", "error_message"} {
		if !db.Migrator().HasColumn("usage_events", column) {
			t.Fatalf("expected usage_events.%s column to exist", column)
		}
	}

	var statusCode int
	var errorType, errorMessage string
	if err := db.Raw(`SELECT status_code, error_type, error_message FROM usage_events WHERE id = ?`, int64(1)).Row().Scan(&statusCode, &errorType, &errorMessage); err != nil {
		t.Fatalf("scan error detail fields: %v", err)
	}
	if statusCode != 0 || errorType != "" || errorMessage != "" {
		t.Fatalf("unexpected defaults: status=%d type=%q message=%q", statusCode, errorType, errorMessage)
	}
}
