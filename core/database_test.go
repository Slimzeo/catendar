package core

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDatabaseMigratesLegacyEventTableBeforeCreatingSourceIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calendar.db")
	legacy, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			start TEXT NOT NULL,
			end TEXT,
			color TEXT NOT NULL,
			all_day INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		legacy.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := legacy.Exec(`
		INSERT INTO events (title, start, color, all_day)
		VALUES ('Legacy task', '2026-09-01T00:00:00', '#7663b5', 1)
	`); err != nil {
		legacy.Close()
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := openDatabase(path)
	if err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	for _, column := range []string{
		"description", "result", "bold", "phase", "deadline", "source_type",
		"source_account_id", "source_message_id", "source_url",
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = ?", column).Scan(&count); err != nil {
			db.Close()
			t.Fatalf("inspect column %s: %v", column, err)
		}
		if count != 1 {
			db.Close()
			t.Fatalf("column %s was not migrated", column)
		}
	}

	var title, start, color, phase, deadline, result string
	if err := db.QueryRow(`
		SELECT title, start, color, phase, deadline, result FROM events WHERE id = 1
	`).Scan(&title, &start, &color, &phase, &deadline, &result); err != nil {
		db.Close()
		t.Fatalf("read legacy row: %v", err)
	}
	if title != "Legacy task" || start != "2026-09-01T00:00:00" || color != "#7663b5" {
		db.Close()
		t.Fatalf("legacy row changed: title=%q start=%q color=%q", title, start, color)
	}
	if phase != "todo" || deadline != "" || result != "" {
		db.Close()
		t.Fatalf("legacy row defaults = phase %q deadline %q result %q", phase, deadline, result)
	}

	const migratedResult = "迁移后\n保留结果"
	if _, err := db.Exec("UPDATE events SET result = ? WHERE id = 1", migratedResult); err != nil {
		db.Close()
		t.Fatalf("set migrated result: %v", err)
	}

	var indexCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_events_email_source'").Scan(&indexCount); err != nil {
		db.Close()
		t.Fatalf("inspect source index: %v", err)
	}
	if indexCount != 1 {
		db.Close()
		t.Fatal("email source index was not created")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}

	reopened, err := openDatabase(path)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer reopened.Close()
	for _, column := range []string{"phase", "deadline", "result"} {
		var count int
		if err := reopened.QueryRow("SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = ?", column).Scan(&count); err != nil {
			t.Fatalf("re-inspect column %s: %v", column, err)
		}
		if count != 1 {
			t.Fatalf("column %s was duplicated or removed on reopen", column)
		}
	}
	if err := reopened.QueryRow("SELECT phase, deadline, result FROM events WHERE id = 1").Scan(&phase, &deadline, &result); err != nil {
		t.Fatalf("read legacy row after reopen: %v", err)
	}
	if phase != "todo" || deadline != "" || result != migratedResult {
		t.Fatalf("legacy row changed after reopen: phase=%q deadline=%q result=%q", phase, deadline, result)
	}
	var eventCount int
	if err := reopened.QueryRow("SELECT COUNT(*) FROM events").Scan(&eventCount); err != nil {
		t.Fatalf("count events after reopen: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("event count after reopen = %d, want 1", eventCount)
	}
}

func TestPrepareDataDirectoryMigratesLegacyDirectory(t *testing.T) {
	configDir := t.TempDir()
	canonical := filepath.Join(configDir, dataDirectoryName)
	legacy := filepath.Join(configDir, legacyDataDirectoryName)

	writeDataDirectoryTestFile(t, filepath.Join(legacy, databaseFileName), "legacy database")
	writeDataDirectoryTestFile(t, filepath.Join(legacy, databaseFileName+"-wal"), "legacy wal")
	writeDataDirectoryTestFile(t, filepath.Join(legacy, databaseFileName+"-shm"), "legacy shm")
	writeDataDirectoryTestFile(t, filepath.Join(legacy, "notes.txt"), "unique notes")

	canonicalInfo, canonicalErr := os.Stat(canonical)
	legacyInfo, err := os.Stat(legacy)
	if err != nil {
		t.Fatalf("inspect legacy directory: %v", err)
	}
	sameFile := canonicalErr == nil && os.SameFile(canonicalInfo, legacyInfo)

	dir, err := PrepareDataDirectory(configDir)
	if err != nil {
		t.Fatalf("prepare data directory: %v", err)
	}
	if dir != canonical {
		t.Fatalf("data directory = %q, want %q", dir, canonical)
	}
	for name, want := range map[string]string{
		databaseFileName:          "legacy database",
		databaseFileName + "-wal": "legacy wal",
		databaseFileName + "-shm": "legacy shm",
		"notes.txt":               "unique notes",
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read migrated %s: %v", name, err)
		}
		if string(data) != want {
			t.Fatalf("migrated %s = %q, want %q", name, data, want)
		}
	}
	if !sameFile {
		if _, err := os.Stat(legacy); !os.IsNotExist(err) {
			t.Fatalf("legacy directory still exists: %v", err)
		}
	}

	again, err := PrepareDataDirectory(configDir)
	if err != nil {
		t.Fatalf("prepare data directory again: %v", err)
	}
	if again != canonical {
		t.Fatalf("second data directory = %q, want %q", again, canonical)
	}
}

func TestDataDirectoryKeepsDistinctDirectoriesIntact(t *testing.T) {
	// Distinct names exercise a case-sensitive installation even on macOS.
	root := t.TempDir()
	current, legacy := filepath.Join(root, "new"), filepath.Join(root, "old")
	writeDataDirectoryTestFile(t, filepath.Join(current, "notes"), "kept")
	writeDataDirectoryTestFile(t, filepath.Join(legacy, databaseFileName), "old database")
	writeDataDirectoryTestFile(t, filepath.Join(legacy, databaseFileName+"-wal"), "old WAL")
	for _, want := range []string{legacy, current} {
		if want == current {
			writeDataDirectoryTestFile(t, filepath.Join(current, databaseFileName), "new database")
		}
		dir, err := resolveDataDirectory(current, legacy)
		if err != nil || dir != want {
			t.Fatalf("directory = %q, %v; want %q", dir, err, want)
		}
	}
	for path, want := range map[string]string{
		filepath.Join(current, "notes"):                "kept",
		filepath.Join(current, databaseFileName):       "new database",
		filepath.Join(legacy, databaseFileName):        "old database",
		filepath.Join(legacy, databaseFileName+"-wal"): "old WAL",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestPrepareDataDirectoryRejectsLegacyFilePath(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, legacyDataDirectoryName), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create legacy file: %v", err)
	}
	if _, err := PrepareDataDirectory(configDir); err == nil {
		t.Fatal("legacy file path was accepted as a data directory")
	}
}

func TestPrepareDataDirectoryCreatesCanonicalDirectory(t *testing.T) {
	configDir := t.TempDir()
	dir, err := PrepareDataDirectory(configDir)
	if err != nil {
		t.Fatalf("prepare data directory: %v", err)
	}
	want := filepath.Join(configDir, dataDirectoryName)
	if dir != want {
		t.Fatalf("data directory = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("inspect created directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("created data path is not a directory: %s", dir)
	}
}

func writeDataDirectoryTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
