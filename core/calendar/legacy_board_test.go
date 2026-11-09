package calendar

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// This fixture has the old workspace tables and columns. None of them may
// influence which cards are visible or which change a review can approve.
const legacyWorkspaceFixtureSchema = `
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
`

func legacyWorkspaceService(t *testing.T, now time.Time) (*Service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "calendar.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON;" + legacyWorkspaceFixtureSchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := SeedBoard(context.Background(), db, now); err != nil {
		t.Fatalf("seed legacy board: %v", err)
	}
	service := NewService(db)
	service.now = func() time.Time { return now }
	return service, db
}

func insertLegacyWorkspaceEvent(
	t *testing.T,
	db *sql.DB,
	title, start, phase, result string,
	workspaceID int64,
) int64 {
	t.Helper()
	insert, err := db.Exec(`
		INSERT INTO events (
			title, start, end, color, all_day, description, result, bold, phase, deadline,
			workspace_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, 1, '', ?, 0, ?, '', ?, '2026-02-28 00:10:00', '2026-02-28 00:10:00')
	`, title, start, start, defaultEventColor, result, phase, workspaceID)
	if err != nil {
		t.Fatalf("insert legacy event %q: %v", title, err)
	}
	id, err := insert.LastInsertId()
	if err != nil {
		t.Fatalf("read legacy event id: %v", err)
	}
	return id
}

func insertLegacyWorkspaceDay(
	t *testing.T,
	db *sql.DB,
	eventID int64,
	day, title, start, phase string,
	through any,
	workspaceID int64,
) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO event_days (
			event_id, day, through_day, title, start, end, color, all_day,
			description, result, bold, phase, deadline, workspace_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, '', '', 0, ?, '', ?, '2026-02-28 00:10:00', '2026-02-28 00:10:00')
	`, eventID, day, through, title, start, start, defaultEventColor, phase, workspaceID); err != nil {
		t.Fatalf("insert legacy board row for %q: %v", title, err)
	}
}

func TestLegacyWorkspacesStayInertAndDoNotHideOrDuplicateCards(t *testing.T) {
	now := boardTestTime(2026, time.March, 2)
	service, db := legacyWorkspaceService(t, now)
	ctx := context.Background()
	today, yesterday := "2026-03-02", "2026-03-01"

	seekino := insertLegacyWorkspaceEvent(t, db, "Seekino release", today+"T00:00:00", PhaseTodo, "", 2)
	personal := insertLegacyWorkspaceEvent(t, db, "个人 IP 发布", today+"T00:00:00", PhaseTodo, "", 3)
	finished := insertLegacyWorkspaceEvent(t, db, "Yesterday done", yesterday+"T00:00:00", PhaseDone, "finished", 2)
	carried := insertLegacyWorkspaceEvent(t, db, "Carried personal", yesterday+"T00:00:00", PhaseProgressing, "", 3)
	insertLegacyWorkspaceDay(t, db, seekino, today, "Seekino release", today+"T00:00:00", PhaseTodo, nil, 2)
	insertLegacyWorkspaceDay(t, db, personal, today, "个人 IP 发布", today+"T00:00:00", PhaseTodo, nil, 3)
	insertLegacyWorkspaceDay(t, db, finished, yesterday, "Yesterday done", yesterday+"T00:00:00", PhaseDone, yesterday, 2)
	insertLegacyWorkspaceDay(t, db, carried, yesterday, "Carried personal", yesterday+"T00:00:00", PhaseProgressing, nil, 3)
	if _, err := db.Exec(
		"INSERT INTO chat_summary_keys (event_id, task_key) VALUES (?, ?)", seekino, "seekino-release",
	); err != nil {
		t.Fatalf("seed legacy canonical key: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO chat_activities (
			day, state, reason, before_phase, source_id, thread_id, thread_title,
			source_at, quote, task_key, event_id, workspace_id, title, phase,
			description, result, needs_review, base_updated, created_at, resolved_at
		) VALUES
			(?, 'pending', '', 'todo', 'legacy-pending', 'thread-legacy', 'Old project review',
			 ?, 'legacy quote', 'seekino-release', ?, 3, 'Seekino release', 'progressing',
			 '', '', 0, '', ?, NULL),
			(?, 'applied', 'approved by user', 'todo', 'legacy-applied', 'thread-legacy', 'Old project review',
			 ?, 'legacy quote', 'personal-release', ?, 2, '个人 IP 发布', 'todo',
			 '', '', 0, '', ?, ?)
	`, today, now.UnixMilli(), seekino, now,
		today, now.Add(-time.Minute).UnixMilli(), personal, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("insert legacy chat activities: %v", err)
	}

	todayBoard := mustBoard(t, service, today)
	todayIDs := boardEventsByID(t, todayBoard)
	for _, id := range []int64{seekino, personal, carried} {
		if _, ok := todayIDs[id]; !ok {
			t.Fatalf("today's project card %d is missing: %#v", id, todayBoard)
		}
	}
	if _, ok := todayIDs[finished]; ok {
		t.Fatalf("finished historical card %d is visible today: %#v", finished, todayBoard)
	}
	if len(todayBoard) != 3 {
		t.Fatalf("today board = %d cards, want all live project cards: %#v", len(todayBoard), todayBoard)
	}
	if todayIDs[carried].Start != yesterday+"T00:00:00" || todayIDs[carried].Phase != PhaseProgressing {
		t.Fatalf("carried card lost its identity or phase: %#v", todayIDs[carried])
	}

	planning, err := service.GetChatPlanningEvents(ctx, today)
	if err != nil || len(planning) != 3 {
		t.Fatalf("planning events = %#v err=%v", planning, err)
	}
	if planningKey(planning, seekino) != "seekino-release" {
		t.Fatalf("canonical key missing from planning events: %#v", planning)
	}

	yesterdayBoard := mustBoard(t, service, yesterday)
	yesterdayIDs := boardEventsByID(t, yesterdayBoard)
	for _, id := range []int64{finished, carried} {
		if _, ok := yesterdayIDs[id]; !ok {
			t.Fatalf("yesterday's card %d is missing: %#v", id, yesterdayBoard)
		}
	}
	if yesterdayIDs[finished].Phase != PhaseDone || yesterdayIDs[carried].Phase != PhaseProgressing {
		t.Fatalf("past phases changed: %#v", yesterdayBoard)
	}
	if len(yesterdayBoard) != 2 {
		t.Fatalf("yesterday board = %d cards: %#v", len(yesterdayBoard), yesterdayBoard)
	}

	pending := pendingChatActivity(t, service)
	if pending.Change.EventID != seekino || pending.Current == nil || pending.Current.ID != seekino {
		t.Fatalf("pending legacy review = %#v", pending)
	}
	approved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil {
		t.Fatalf("approve legacy review: %v", err)
	}
	if approved.State != chatStateApplied {
		t.Fatalf("legacy review state = %q, reason %q", approved.State, approved.Reason)
	}
	updated, err := eventByID(ctx, service.db, seekino)
	if err != nil || updated.Phase != PhaseProgressing || updated.ID != seekino {
		t.Fatalf("approved legacy card = %#v err=%v", updated, err)
	}

	var eventCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&eventCount); err != nil || eventCount != 4 {
		t.Fatalf("event count after legacy approval = %d err=%v", eventCount, err)
	}
	var workspaceID int64
	if err := db.QueryRow("SELECT workspace_id FROM events WHERE id = ?", seekino).Scan(&workspaceID); err != nil || workspaceID != 2 {
		t.Fatalf("inert legacy event workspace = %d err=%v", workspaceID, err)
	}
	var workspaceCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM workspaces").Scan(&workspaceCount); err != nil || workspaceCount != 3 {
		t.Fatalf("legacy workspaces = %d err=%v", workspaceCount, err)
	}
	var workspaceName string
	if err := db.QueryRow("SELECT name FROM workspaces WHERE id = 2").Scan(&workspaceName); err != nil || workspaceName != "Seekino" {
		t.Fatalf("legacy workspace name = %q err=%v", workspaceName, err)
	}

	activities, err := service.ListChatActivity(ctx)
	if err != nil {
		t.Fatalf("list legacy activity: %v", err)
	}
	var applied bool
	for _, activity := range activities {
		if activity.Change.Source.ID == "legacy-applied" && activity.State == chatStateApplied {
			applied = true
		}
	}
	if !applied {
		t.Fatalf("previously applied legacy change was lost: %#v", activities)
	}
}

func planningKey(events []Event, id int64) string {
	for _, event := range events {
		if event.ID == id {
			return event.SummaryKey
		}
	}
	return ""
}

func TestFreshSchemaHasNoWorkspaceTablesOrColumns(t *testing.T) {
	service := testService(t)
	for _, table := range []string{"workspaces"} {
		var count int
		if err := service.db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&count); err != nil || count != 0 {
			t.Fatalf("fresh schema table %s = %d err=%v", table, count, err)
		}
	}
	for table, columns := range map[string][]string{
		"events":          {"workspace_id"},
		"event_days":      {"workspace_id"},
		"chat_activities": {"workspace_id"},
	} {
		for _, column := range columns {
			var count int
			if err := service.db.QueryRow(
				"SELECT COUNT(*) FROM pragma_table_info('"+table+"') WHERE name = ?", column,
			).Scan(&count); err != nil || count != 0 {
				t.Fatalf("fresh schema %s.%s = %d err=%v", table, column, count, err)
			}
		}
	}
}
