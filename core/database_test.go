package core

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"catendar-wails/core/calendar"
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
	var workspaceTable int
	if err := reopened.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'workspaces'",
	).Scan(&workspaceTable); err != nil || workspaceTable != 0 {
		t.Fatalf("fresh database workspace table = %d err=%v", workspaceTable, err)
	}
	var workspaceColumn int
	if err := reopened.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'workspace_id'",
	).Scan(&workspaceColumn); err != nil || workspaceColumn != 0 {
		t.Fatalf("fresh database workspace column = %d err=%v", workspaceColumn, err)
	}
}

func TestOpenDatabaseSeedsBoardDaysForRecentWorkOnly(t *testing.T) {
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
			description TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			bold INTEGER NOT NULL DEFAULT 0,
			phase TEXT NOT NULL DEFAULT 'todo',
			deadline TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			source_account_id TEXT NOT NULL DEFAULT '',
			source_message_id TEXT NOT NULL DEFAULT '',
			source_url TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		legacy.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	insert := func(title, start, phase, updatedAt string) int64 {
		t.Helper()
		result, err := legacy.Exec(`
			INSERT INTO events (title, start, end, color, all_day, phase, created_at, updated_at)
			VALUES (?, ?, ?, '#7663b5', 1, ?, ?, ?)
		`, title, start, start, phase, updatedAt, updatedAt)
		if err != nil {
			legacy.Close()
			t.Fatalf("insert %s: %v", title, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			legacy.Close()
			t.Fatalf("read %s id: %v", title, err)
		}
		return id
	}
	yTodo := insert("Yesterday todo", "2026-03-01T00:00:00", "todo", "2026-03-01 00:30:00")
	yProgress := insert("Yesterday progress", "2026-03-01T00:00:00", "progressing", "2026-03-01 00:30:00")
	yDone := insert("Yesterday done", "2026-03-01T00:00:00", "done", "2026-03-01 00:30:00")
	oldWorked := insert("Older worked", "2026-01-10T00:00:00", "todo", "2026-02-28 23:00:00")
	oldUntouched := insert("Older untouched", "2026-01-10T00:00:00", "todo", "2026-01-10 00:00:00")
	future := insert("Future", "2026-03-03T00:00:00", "todo", "2026-03-01 00:30:00")
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	now := time.Date(2026, time.March, 2, 9, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	db, err := openDatabaseAt(path, now)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	for id, wantDay := range map[int64]string{
		yTodo:     "2026-03-01",
		yProgress: "2026-03-01",
		yDone:     "2026-03-01",
		oldWorked: "2026-03-01",
		future:    "2026-03-03",
	} {
		var day string
		if err := db.QueryRow(`SELECT day FROM event_days WHERE event_id = ?`, id).Scan(&day); err != nil {
			db.Close()
			t.Fatalf("read seeded day for %d: %v", id, err)
		}
		if day != wantDay {
			db.Close()
			t.Fatalf("seeded day for %d = %q, want %q", id, day, wantDay)
		}
	}
	var untouchedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM event_days WHERE event_id = ?`, oldUntouched).Scan(&untouchedCount); err != nil {
		db.Close()
		t.Fatalf("count untouched history: %v", err)
	}
	if untouchedCount != 0 {
		db.Close()
		t.Fatalf("untouched history was enrolled %d times", untouchedCount)
	}
	var seededAt sql.NullString
	if err := db.QueryRow(`SELECT seeded_at FROM board_meta WHERE id = 1`).Scan(&seededAt); err != nil {
		db.Close()
		t.Fatalf("read board seed marker: %v", err)
	}
	if !seededAt.Valid {
		db.Close()
		t.Fatal("board seed marker was not recorded")
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM event_days`).Scan(&rowCount); err != nil {
		db.Close()
		t.Fatalf("count seeded days: %v", err)
	}
	if rowCount != 5 {
		db.Close()
		t.Fatalf("seeded day count = %d, want 5", rowCount)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}

	reopened, err := openDatabaseAt(path, now.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer reopened.Close()
	var reopenedCount int
	if err := reopened.QueryRow(`SELECT COUNT(*) FROM event_days`).Scan(&reopenedCount); err != nil {
		t.Fatalf("count reopened days: %v", err)
	}
	if reopenedCount != rowCount {
		t.Fatalf("no-change relaunch added board rows: before=%d after=%d", rowCount, reopenedCount)
	}
	var phase string
	if err := reopened.QueryRow(`SELECT phase FROM event_days WHERE event_id = ? AND day = ?`, yTodo, "2026-03-01").Scan(&phase); err != nil {
		t.Fatalf("read reopened phase: %v", err)
	}
	if phase != "todo" {
		t.Fatalf("no-change relaunch rewrote yesterday's phase to %q", phase)
	}
	if _, err := reopened.Exec(`DELETE FROM events WHERE id = ?`, yTodo); err != nil {
		t.Fatalf("delete seeded event: %v", err)
	}
	var cascaded int
	if err := reopened.QueryRow(`SELECT COUNT(*) FROM event_days WHERE event_id = ?`, yTodo).Scan(&cascaded); err != nil {
		t.Fatalf("count deleted board rows: %v", err)
	}
	if cascaded != 0 {
		t.Fatalf("event deletion left %d board rows", cascaded)
	}
}

func TestOpenDatabaseKeepsLegacyWorkspaceCardsAndReviewsWorking(t *testing.T) {
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	path := filepath.Join(t.TempDir(), "calendar.db")
	legacy, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE workspaces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			hint TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO workspaces (id, name, hint) VALUES
			(1, 'Daily Routine', ''),
			(2, 'Seekino', ''),
			(3, '个人 IP 打造', '');
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			start TEXT NOT NULL,
			end TEXT,
			color TEXT NOT NULL,
			all_day INTEGER NOT NULL DEFAULT 1,
			description TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			bold INTEGER NOT NULL DEFAULT 0,
			phase TEXT NOT NULL DEFAULT 'todo',
			deadline TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			source_account_id TEXT NOT NULL DEFAULT '',
			source_message_id TEXT NOT NULL DEFAULT '',
			source_url TEXT NOT NULL DEFAULT '',
			workspace_id INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE event_days (
			event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
			day TEXT NOT NULL, through_day TEXT,
			title TEXT NOT NULL, start TEXT NOT NULL, end TEXT, color TEXT NOT NULL,
			all_day INTEGER NOT NULL, description TEXT NOT NULL, result TEXT NOT NULL,
			bold INTEGER NOT NULL, phase TEXT NOT NULL, deadline TEXT NOT NULL,
			workspace_id INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME, updated_at DATETIME,
			PRIMARY KEY (event_id, day)
		);
		CREATE TABLE board_meta (
			id INTEGER PRIMARY KEY CHECK (id = 1), seeded_at DATETIME NOT NULL
		);
		CREATE TABLE chat_activities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			day TEXT NOT NULL,
			state TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			before_phase TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL,
			thread_id TEXT NOT NULL DEFAULT '',
			thread_title TEXT NOT NULL DEFAULT '',
			source_at INTEGER NOT NULL DEFAULT 0,
			quote TEXT NOT NULL DEFAULT '',
			task_key TEXT NOT NULL,
			event_id INTEGER NOT NULL DEFAULT 0,
			workspace_id INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL,
			phase TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			needs_review INTEGER NOT NULL DEFAULT 0,
			base_updated TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			resolved_at DATETIME
		);
	`); err != nil {
		legacy.Close()
		t.Fatalf("create legacy board schema: %v", err)
	}
	insertEvent := func(title, start, phase, result string, workspaceID int64) int64 {
		t.Helper()
		insert, err := legacy.Exec(`
			INSERT INTO events (
				title, start, end, color, all_day, description, result, bold, phase, deadline,
				workspace_id, created_at, updated_at
			) VALUES (?, ?, ?, '#7663b5', 1, '', ?, 0, ?, '', ?, '2026-03-02 00:10:00', '2026-03-02 00:10:00')
		`, title, start, start, result, phase, workspaceID)
		if err != nil {
			legacy.Close()
			t.Fatalf("insert legacy event %q: %v", title, err)
		}
		id, err := insert.LastInsertId()
		if err != nil {
			legacy.Close()
			t.Fatalf("read legacy event id: %v", err)
		}
		return id
	}
	insertDay := func(eventID int64, day, title, start, phase string, through any, workspaceID int64) {
		t.Helper()
		if _, err := legacy.Exec(`
			INSERT INTO event_days (
				event_id, day, through_day, title, start, end, color, all_day,
				description, result, bold, phase, deadline, workspace_id, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, '#7663b5', 1, '', '', 0, ?, '', ?, '2026-03-02 00:10:00', '2026-03-02 00:10:00')
		`, eventID, day, through, title, start, start, phase, workspaceID); err != nil {
			legacy.Close()
			t.Fatalf("insert legacy board row for %q: %v", title, err)
		}
	}
	seekino := insertEvent("Seekino release", today+"T00:00:00", "todo", "", 2)
	personal := insertEvent("个人 IP 发布", today+"T00:00:00", "progressing", "", 3)
	finished := insertEvent("Yesterday done", yesterday+"T00:00:00", "done", "finished", 2)
	carried := insertEvent("Carried personal", yesterday+"T00:00:00", "progressing", "", 3)
	insertDay(seekino, today, "Seekino release", today+"T00:00:00", "todo", nil, 2)
	insertDay(personal, today, "个人 IP 发布", today+"T00:00:00", "progressing", nil, 3)
	insertDay(finished, yesterday, "Yesterday done", yesterday+"T00:00:00", "done", yesterday, 2)
	insertDay(carried, yesterday, "Carried personal", yesterday+"T00:00:00", "progressing", nil, 3)
	if _, err := legacy.Exec(`
		INSERT INTO board_meta (id, seeded_at) VALUES (1, '2026-03-02 00:10:00')
	`); err != nil {
		legacy.Close()
		t.Fatalf("insert legacy board marker: %v", err)
	}
	if _, err := legacy.Exec(`
		INSERT INTO chat_activities (
			day, state, reason, before_phase, source_id, thread_id, thread_title,
			source_at, quote, task_key, event_id, workspace_id, title, phase,
			description, result, needs_review, base_updated, created_at, resolved_at
		) VALUES
			(?, 'pending', '', 'todo', 'legacy-pending', 'thread-legacy', 'Old project review',
			 ?, 'legacy quote', 'seekino-release', ?, 3, 'Seekino release', 'progressing',
			 '', '', 0, '', ?, NULL),
			(?, 'applied', 'approved by user', 'todo', 'legacy-applied', 'thread-legacy', 'Old project review',
			 ?, 'legacy quote', 'personal-release', ?, 2, '个人 IP 发布', 'progressing',
			 '', '', 0, '', ?, ?)
	`, today, now.UnixMilli(), seekino, now,
		today, now.Add(-time.Minute).UnixMilli(), personal, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		legacy.Close()
		t.Fatalf("insert legacy chat activities: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := openDatabaseAt(path, now)
	if err != nil {
		t.Fatalf("open existing database: %v", err)
	}
	defer db.Close()
	service := calendar.NewService(db)

	board, err := service.GetBoardEvents(context.Background(), today)
	if err != nil {
		t.Fatalf("today board: %v", err)
	}
	if len(board) != 3 {
		t.Fatalf("today board = %#v, want all live project cards", board)
	}
	boardIDs := make(map[int64]calendar.Event, len(board))
	for _, event := range board {
		boardIDs[event.ID] = event
	}
	for _, id := range []int64{seekino, personal, carried} {
		if _, ok := boardIDs[id]; !ok {
			t.Fatalf("today's card %d is missing: %#v", id, board)
		}
	}
	if _, ok := boardIDs[finished]; ok {
		t.Fatalf("finished historical card is visible today: %#v", board)
	}
	if boardIDs[carried].Start != yesterday+"T00:00:00" || boardIDs[carried].Phase != calendar.PhaseProgressing {
		t.Fatalf("carried card changed: %#v", boardIDs[carried])
	}
	yesterdayBoard, err := service.GetBoardEvents(context.Background(), yesterday)
	if err != nil || len(yesterdayBoard) != 2 {
		t.Fatalf("yesterday board = %#v err=%v", yesterdayBoard, err)
	}
	for _, event := range yesterdayBoard {
		if event.ID == finished && event.Phase != calendar.PhaseDone {
			t.Fatalf("past completion changed: %#v", event)
		}
	}

	activities, err := service.ListChatActivity(context.Background())
	if err != nil {
		t.Fatalf("list legacy activity: %v", err)
	}
	var pending *calendar.ChatActivity
	for i := range activities {
		if activities[i].Change.Source.ID == "legacy-pending" && activities[i].State == "pending" {
			pending = &activities[i]
		}
	}
	if pending == nil || pending.Change.EventID != seekino {
		t.Fatalf("pending legacy review = %#v", activities)
	}
	approved, err := service.ResolveChatChange(context.Background(), pending.ID, true)
	if err != nil {
		t.Fatalf("approve legacy review: %v", err)
	}
	if approved.State != "applied" {
		t.Fatalf("legacy review state = %q, reason %q", approved.State, approved.Reason)
	}

	var eventCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&eventCount); err != nil || eventCount != 4 {
		t.Fatalf("event count after legacy approval = %d err=%v", eventCount, err)
	}
	var workspaceCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM workspaces").Scan(&workspaceCount); err != nil || workspaceCount != 3 {
		t.Fatalf("legacy workspaces = %d err=%v", workspaceCount, err)
	}
	var eventWorkspace int64
	if err := db.QueryRow("SELECT workspace_id FROM events WHERE id = ?", seekino).Scan(&eventWorkspace); err != nil || eventWorkspace != 2 {
		t.Fatalf("legacy event workspace = %d err=%v", eventWorkspace, err)
	}
	var summaryObjects int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master WHERE name IN ('chat_summary_keys', 'idx_chat_summary_keys_task_key')
	`).Scan(&summaryObjects); err != nil || summaryObjects != 2 {
		t.Fatalf("chat summary key schema = %d err=%v", summaryObjects, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close existing database: %v", err)
	}

	reopened, err := openDatabaseAt(path, now.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("reopen existing database: %v", err)
	}
	defer reopened.Close()
	if err := reopened.QueryRow("SELECT COUNT(*) FROM events").Scan(&eventCount); err != nil || eventCount != 4 {
		t.Fatalf("event count after reopen = %d err=%v", eventCount, err)
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
