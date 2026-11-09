package calendar

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func testService(t *testing.T) *Service {
	t.Helper()
	return testServiceAt(t, filepath.Join(t.TempDir(), "calendar.db"))
}

func testServiceAt(t *testing.T, path string) *Service {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	createEventTestSchema(t, db)
	if _, err := db.Exec("PRAGMA foreign_keys = ON;" + boardSchema + chatSchema); err != nil {
		t.Fatalf("create board schema: %v", err)
	}
	return NewService(db)
}

func createEventTestSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
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
		CREATE UNIQUE INDEX IF NOT EXISTS idx_events_email_source
		ON events(source_account_id, source_message_id, title, start)
		WHERE source_type = 'email';
	`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

}

func eventTestStringPointer(value string) *string {
	return &value
}

func eventTestInput(event Event) EventInput {
	return EventInput{
		Title:       event.Title,
		Start:       event.Start,
		End:         event.End,
		Color:       event.Color,
		AllDay:      event.AllDay,
		Description: event.Description,
		Bold:        event.Bold,
		Phase:       event.Phase,
	}
}

func TestGetEventsByDateRangeIncludesEntireEndDate(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	for _, input := range []EventInput{
		{Title: "Last day", Start: "2026-09-30T23:30:00", End: "2026-09-30T23:45:00", Color: defaultEventColor},
		{Title: "Next month", Start: "2026-10-01T00:00:00", End: "2026-10-01T00:30:00", Color: defaultEventColor},
	} {
		if _, err := service.CreateEvent(ctx, input); err != nil {
			t.Fatalf("create %q: %v", input.Title, err)
		}
	}

	events, err := service.GetEventsByDateRange(ctx, "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 || events[0].Title != "Last day" {
		t.Fatalf("expected only the end-date event, got %#v", events)
	}
}

func TestCreateEventDefaultsToTodoPhase(t *testing.T) {
	service := testService(t)
	ctx := context.Background()

	defaults, err := service.CreateEvent(ctx, EventInput{
		Title:       "All day default",
		Start:       "2026-09-08T00:00:00",
		End:         "2026-09-08T00:00:00",
		Color:       defaultEventColor,
		AllDay:      true,
		Description: "Default phase",
	})
	if err != nil {
		t.Fatalf("create default event: %v", err)
	}
	if defaults.Phase != PhaseTodo || defaults.Deadline != "" {
		t.Fatalf("unexpected default fields: %#v", defaults)
	}

	timed, err := service.CreateEvent(ctx, EventInput{
		Title:  "Timed event",
		Start:  "2026-09-08T14:00:00",
		End:    "2026-09-08T15:00:00",
		Color:  defaultEventColor,
		AllDay: false,
	})
	if err != nil {
		t.Fatalf("create timed event: %v", err)
	}
	if timed.Phase != PhaseTodo {
		t.Fatalf("timed event phase = %q, want %q", timed.Phase, PhaseTodo)
	}
}

func TestCreateEventSupportsTitleOnlyInput(t *testing.T) {
	service := testService(t)
	ctx := context.Background()

	before := time.Now().Format(dateLayout)
	event, err := service.CreateEvent(ctx, EventInput{Title: "  Write daily report  "})
	after := time.Now().Format(dateLayout)
	if err != nil {
		t.Fatalf("create title-only event: %v", err)
	}
	if event.Title != "Write daily report" {
		t.Fatalf("title = %q, want trimmed title", event.Title)
	}
	if event.Start != before+"T00:00:00" && event.Start != after+"T00:00:00" {
		t.Fatalf("start = %q, want today's local date at midnight", event.Start)
	}
	if !event.AllDay {
		t.Fatal("title-only event was not marked all-day")
	}
	if event.End != event.Start {
		t.Fatalf("end = %q, want title-only event to end at its start", event.End)
	}
	if event.Color != defaultEventColor {
		t.Fatalf("color = %q, want default color", event.Color)
	}
	if event.Phase != PhaseTodo {
		t.Fatalf("phase = %q, want %q", event.Phase, PhaseTodo)
	}
	if event.Deadline != "" {
		t.Fatalf("deadline = %q, want empty", event.Deadline)
	}
}

func TestCreateEventRejectsBlankTitle(t *testing.T) {
	service := testService(t)
	if _, err := service.CreateEvent(context.Background(), EventInput{Title: "   "}); err == nil {
		t.Fatal("blank title was accepted")
	}
}

func TestCreateEventValidatesPhaseAndDeadline(t *testing.T) {
	service := testService(t)
	ctx := context.Background()

	valid := EventInput{Title: "Valid", Start: "2026-09-08T00:00:00", Color: defaultEventColor}
	for _, input := range []EventInput{
		{Title: "Bad phase", Start: "2026-09-08T00:00:00", Color: defaultEventColor, Phase: "blocked"},
		{Title: "Bad date", Start: "2026-09-08T00:00:00", Color: defaultEventColor, Deadline: eventTestStringPointer("2026-02-30")},
		{Title: "Bad format", Start: "2026-09-08T00:00:00", Color: defaultEventColor, Deadline: eventTestStringPointer("2026-9-8")},
		{Title: "Bad time", Start: "2026-09-08T00:00:00", Color: defaultEventColor, Deadline: eventTestStringPointer("2026-09-08T12:00:00")},
	} {
		if _, err := service.CreateEvent(ctx, input); err == nil {
			t.Fatalf("invalid input %q was accepted", input.Title)
		}
	}

	input := valid
	input.Phase = PhaseProgressing
	input.Deadline = eventTestStringPointer("2028-02-29")
	event, err := service.CreateEvent(ctx, input)
	if err != nil {
		t.Fatalf("create valid event: %v", err)
	}
	if event.Phase != PhaseProgressing || event.Deadline != "2028-02-29" {
		t.Fatalf("unexpected persisted fields: %#v", event)
	}

	events, err := service.GetEventsByDateRange(ctx, "2026-09-08", "2026-09-08")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("invalid creates persisted events: %#v", events)
	}
}

func TestCreateEventTrimsAndPersistsMultilineResult(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	const result = "第一行\nsecond line\n第三行"

	created, err := service.CreateEvent(ctx, EventInput{
		Title:  "Release notes",
		Start:  "2026-09-08T00:00:00",
		Color:  defaultEventColor,
		Result: eventTestStringPointer(" \n" + result + "\n "),
	})
	if err != nil {
		t.Fatalf("create event with result: %v", err)
	}
	if created.Result != result {
		t.Fatalf("result = %q, want %q", created.Result, result)
	}

	events, err := service.GetEventsByDateRange(ctx, "2026-09-08", "2026-09-08")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 || events[0].Result != result {
		t.Fatalf("range read did not return the result: %#v", events)
	}

	withoutResult, err := service.CreateEvent(ctx, EventInput{
		Title: "No result",
		Start: "2026-09-09T00:00:00",
		Color: defaultEventColor,
	})
	if err != nil {
		t.Fatalf("create event without result: %v", err)
	}
	if withoutResult.Result != "" {
		t.Fatalf("result = %q, want empty", withoutResult.Result)
	}
}

func TestUpdateEventResultOmittedPreservesAndExplicitClear(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	created, err := service.CreateEvent(ctx, EventInput{
		Title:  "Result task",
		Start:  "2026-09-08T00:00:00",
		Color:  defaultEventColor,
		Phase:  PhaseProgressing,
		Result: eventTestStringPointer("Initial\nresult"),
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	input := eventTestInput(*created)
	input.Title = "Renamed task"
	input.Phase = PhaseTodo
	updated, err := service.UpdateEvent(ctx, created.ID, input)
	if err != nil {
		t.Fatalf("update without result: %v", err)
	}
	if updated.Result != "Initial\nresult" || updated.Phase != PhaseTodo {
		t.Fatalf("omitted result = %q phase = %q, want preserved result in todo", updated.Result, updated.Phase)
	}

	input = eventTestInput(*updated)
	input.Phase = PhaseDone
	input.Result = eventTestStringPointer("  Updated\n结果  ")
	updated, err = service.UpdateEvent(ctx, created.ID, input)
	if err != nil {
		t.Fatalf("update result: %v", err)
	}
	if updated.Result != "Updated\n结果" || updated.Phase != PhaseDone {
		t.Fatalf("updated result = %q phase = %q, want trimmed multiline result and done phase", updated.Result, updated.Phase)
	}

	input = eventTestInput(*updated)
	input.Result = eventTestStringPointer("")
	cleared, err := service.UpdateEvent(ctx, created.ID, input)
	if err != nil {
		t.Fatalf("clear result: %v", err)
	}
	if cleared.Result != "" {
		t.Fatalf("cleared result = %q, want empty", cleared.Result)
	}
}

func TestSetEventPhaseOnlyChangesPhase(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	created, err := service.CreateEvent(ctx, EventInput{
		Title:       "Move me",
		Start:       "2026-09-08T09:00:00",
		End:         "2026-09-08T10:00:00",
		Color:       "#123456",
		AllDay:      false,
		Description: "Keep every field",
		Result:      eventTestStringPointer("Phase-safe\nresult"),
		Bold:        true,
		Deadline:    eventTestStringPointer("2026-09-30"),
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	updated, err := service.SetEventPhase(ctx, created.ID, PhaseProgressing)
	if err != nil {
		t.Fatalf("set phase: %v", err)
	}
	if updated.Phase != PhaseProgressing {
		t.Fatalf("phase = %q, want %q", updated.Phase, PhaseProgressing)
	}
	if updated.ID != created.ID || updated.Title != created.Title || updated.Start != created.Start ||
		updated.End != created.End || updated.Color != created.Color || updated.AllDay != created.AllDay ||
		updated.Description != created.Description || updated.Bold != created.Bold || updated.Deadline != created.Deadline ||
		updated.Result != created.Result {
		t.Fatalf("phase update changed other fields: before=%#v after=%#v", created, updated)
	}

	if _, err := service.SetEventPhase(ctx, created.ID, "blocked"); err == nil {
		t.Fatal("invalid phase was accepted")
	}
	if _, err := service.SetEventPhase(ctx, 9999, PhaseDone); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown event error = %v, want sql.ErrNoRows", err)
	}
}

func TestUpdateEventPreservesOmittedPhaseAndDeadline(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	created, err := service.CreateEvent(ctx, EventInput{
		Title:       "Original task",
		Start:       "2026-09-08T09:00:00",
		Color:       defaultEventColor,
		End:         "2026-09-08T09:30:00",
		Description: "Original description",
		Phase:       PhaseProgressing,
		Deadline:    eventTestStringPointer("2026-09-30"),
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	input := eventTestInput(*created)
	input.Phase = ""
	input.Deadline = nil
	input.Title = "Renamed task"
	input.Start = "2026-09-09T09:00:00"
	input.End = "2026-09-09T09:30:00"

	updated, err := service.UpdateEvent(ctx, created.ID, input)
	if err != nil {
		t.Fatalf("update event: %v", err)
	}
	if updated.Title != "Renamed task" || updated.Start != "2026-09-09T09:00:00" {
		t.Fatalf("scheduling fields were not updated: %#v", updated)
	}
	if updated.Phase != PhaseProgressing {
		t.Fatalf("phase = %q, want preserved %q", updated.Phase, PhaseProgressing)
	}
	if updated.Deadline != "2026-09-30" {
		t.Fatalf("deadline = %q, want preserved deadline", updated.Deadline)
	}

	if _, err := service.UpdateEvent(ctx, 9999, input); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown event error = %v, want sql.ErrNoRows", err)
	}
}

func TestUpdateEventClearsDeadlineAndValidatesPhaseAndDeadline(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	created, err := service.CreateEvent(ctx, EventInput{
		Title:    "Deadline task",
		Start:    "2026-09-08T00:00:00",
		Color:    defaultEventColor,
		Deadline: eventTestStringPointer("2026-09-30"),
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	blank := eventTestInput(*created)
	blank.Title = "   "
	if _, err := service.UpdateEvent(ctx, created.ID, blank); err == nil {
		t.Fatal("blank update title was accepted")
	}

	input := eventTestInput(*created)
	input.Deadline = eventTestStringPointer("")
	cleared, err := service.UpdateEvent(ctx, created.ID, input)
	if err != nil {
		t.Fatalf("clear deadline: %v", err)
	}
	if cleared.Deadline != "" {
		t.Fatalf("deadline = %q, want empty", cleared.Deadline)
	}

	input.Deadline = eventTestStringPointer("2026-02-30")
	if _, err := service.UpdateEvent(ctx, created.ID, input); err == nil {
		t.Fatal("invalid deadline was accepted")
	}
	input.Deadline = nil
	input.Phase = "blocked"
	if _, err := service.UpdateEvent(ctx, created.ID, input); err == nil {
		t.Fatal("invalid phase was accepted")
	}
}

func TestBatchCreateFromEmailAddsSourceAndDeduplicates(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	sources := map[string]EmailSource{
		"42:7": {
			AccountID: "primary",
			MessageID: "message@example.com",
			URL:       "catendar://email/primary?uid=7",
		},
	}
	input := []EmailEventInput{{
		Title:         "Design review",
		Start:         "2026-09-08T14:00:00",
		End:           "2026-09-08T15:00:00",
		Description:   "Review the proposal",
		SourceEmailID: "42:7",
	}}

	first, err := service.BatchCreateFromEmail(ctx, input, sources)
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	second, err := service.BatchCreateFromEmail(ctx, input, sources)
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if first.Created != 1 || second.Skipped != 1 || second.Conflicts != 0 {
		t.Fatalf("unexpected batch results: first=%+v second=%+v", first, second)
	}

	events, err := service.GetEventsByDateRange(ctx, "2026-09-08", "2026-09-08")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].Description, sources["42:7"].URL) {
		t.Fatalf("source link missing from event: %#v", events)
	}
	if events[0].Phase != PhaseTodo || events[0].Deadline != "" || events[0].Result != "" {
		t.Fatalf("email event did not start as todo: %#v", events[0])
	}
}

func TestRepeatImportPreservesSameTitleAtDifferentTimesAndBoardProgress(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	sources := map[string]EmailSource{"rounds": {
		AccountID: "primary", MessageID: "interviews@example.com", URL: "catendar://email/primary?uid=7",
	}}
	inputs := []EmailEventInput{
		{Title: "Interview", SourceEmailID: "rounds", Start: "2026-09-08T09:00:00"},
		{Title: "Interview", SourceEmailID: "rounds", Start: "2026-09-08T14:00:00"},
	}
	first, err := service.BatchCreateFromEmail(ctx, inputs, sources)
	if err != nil || first.Created != 2 {
		t.Fatalf("import both rounds: result=%+v err=%v", first, err)
	}
	events, err := service.GetEventsByDateRange(ctx, "2026-09-08", "2026-09-08")
	if err != nil || len(events) != 2 {
		t.Fatalf("get imported rounds: events=%+v err=%v", events, err)
	}
	roundResult := "一面通过\n等待二面安排"
	update := eventTestInput(events[0])
	update.Phase = PhaseProgressing
	update.Result = eventTestStringPointer(roundResult)
	if _, err := service.UpdateEvent(ctx, events[0].ID, update); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetEventPhase(ctx, events[0].ID, PhaseDone); err != nil {
		t.Fatal(err)
	}
	repeat, err := service.BatchCreateFromEmail(ctx, inputs, sources)
	if err != nil || repeat.Created != 0 || repeat.Skipped != 2 {
		t.Fatalf("repeat import: result=%+v err=%v", repeat, err)
	}
	after, err := service.GetEventsByDateRange(ctx, "2026-09-08", "2026-09-08")
	if err != nil || len(after) != 2 || after[0].ID != events[0].ID || after[0].Phase != PhaseDone || after[1].Phase != PhaseTodo {
		t.Fatalf("unexpected rounds after repeat: events=%+v err=%v", after, err)
	}
	if after[0].Result != roundResult {
		t.Fatalf("repeat import changed result: %q", after[0].Result)
	}
}
