package calendar

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

var boardTestLocation = time.FixedZone("UTC+8", 8*60*60)

func boardTestTime(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 9, 30, 0, 0, boardTestLocation)
}

func insertBoardTestEvent(
	t *testing.T,
	db *sql.DB,
	title, start, phase, description, result, deadline, updatedAt string,
) int64 {
	t.Helper()
	insert, err := db.Exec(`
		INSERT INTO events (
			title, start, end, color, all_day, description, result, bold, phase, deadline,
			source_type, source_account_id, source_message_id, source_url, created_at, updated_at
		) VALUES (?, ?, ?, ?, 1, ?, ?, 0, ?, ?, '', '', '', '', ?, ?)
	`, title, start, start, defaultEventColor, description, result, phase, deadline, updatedAt, updatedAt)
	if err != nil {
		t.Fatalf("insert board event %q: %v", title, err)
	}
	id, err := insert.LastInsertId()
	if err != nil {
		t.Fatalf("read board event id: %v", err)
	}
	return id
}

func boardEventsByID(t *testing.T, events []Event) map[int64]Event {
	t.Helper()
	result := make(map[int64]Event, len(events))
	for _, event := range events {
		if _, exists := result[event.ID]; exists {
			t.Fatalf("board returned duplicate event id %d", event.ID)
		}
		result[event.ID] = event
	}
	return result
}

func requireBoardEvent(t *testing.T, events []Event, id int64) Event {
	t.Helper()
	event, ok := boardEventsByID(t, events)[id]
	if !ok {
		t.Fatalf("event %d is missing from board %#v", id, events)
	}
	return event
}

func TestBoardCarriesYesterdayOpenTasksAndKeepsDailyHistory(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	db := service.db

	yTodo := insertBoardTestEvent(t, db, "Yesterday todo", "2026-03-01T00:00:00", PhaseTodo,
		"carry description", "carry result", "2026-03-05", "2026-03-01 00:30:00")
	yProgress := insertBoardTestEvent(t, db, "Yesterday progress", "2026-03-01T00:00:00", PhaseProgressing,
		"", "", "", "2026-03-01 00:30:00")
	yDone := insertBoardTestEvent(t, db, "Yesterday done", "2026-03-01T00:00:00", PhaseDone,
		"", "", "", "2026-03-01 00:30:00")
	oldWorked := insertBoardTestEvent(t, db, "Older worked task", "2026-01-10T00:00:00", PhaseTodo,
		"", "", "", "2026-02-28 23:00:00")
	oldUntouched := insertBoardTestEvent(t, db, "Untouched history", "2026-01-10T00:00:00", PhaseTodo,
		"", "", "", "2026-01-10 00:00:00")
	future := insertBoardTestEvent(t, db, "Future appointment", "2026-03-03T00:00:00", PhaseTodo,
		"", "", "", "2026-03-01 00:30:00")

	if err := SeedBoard(ctx, db, now); err != nil {
		t.Fatalf("seed board: %v", err)
	}

	today, err := service.GetBoardEvents(ctx, "2026-03-02")
	if err != nil {
		t.Fatalf("board today: %v", err)
	}
	if len(today) != 3 {
		t.Fatalf("today board = %d events, want 3: %#v", len(today), today)
	}
	todayByID := boardEventsByID(t, today)
	for _, id := range []int64{yTodo, yProgress, oldWorked} {
		if _, ok := todayByID[id]; !ok {
			t.Fatalf("today board is missing event %d: %#v", id, today)
		}
	}
	for _, id := range []int64{yDone, oldUntouched, future} {
		if _, ok := todayByID[id]; ok {
			t.Fatalf("today board contains event %d that must not carry: %#v", id, today)
		}
	}
	carried := todayByID[yTodo]
	if carried.ID != yTodo || carried.Start != "2026-03-01T00:00:00" || carried.Phase != PhaseTodo {
		t.Fatalf("carried event lost its identity or schedule: %#v", carried)
	}
	if carried.Description != "carry description" || carried.Result != "carry result" || carried.Deadline != "2026-03-05" {
		t.Fatalf("carried event lost daily fields: %#v", carried)
	}

	yesterday, err := service.GetBoardEvents(ctx, "2026-03-01")
	if err != nil {
		t.Fatalf("board yesterday: %v", err)
	}
	if len(yesterday) != 4 {
		t.Fatalf("yesterday board = %d events, want 4: %#v", len(yesterday), yesterday)
	}
	yesterdayByID := boardEventsByID(t, yesterday)
	if yesterdayByID[yDone].Phase != PhaseDone || yesterdayByID[yTodo].Phase != PhaseTodo ||
		yesterdayByID[yProgress].Phase != PhaseProgressing {
		t.Fatalf("yesterday phases were rewritten: %#v", yesterday)
	}
	if _, ok := yesterdayByID[oldUntouched]; ok {
		t.Fatalf("untouched history was enrolled: %#v", yesterday)
	}

	tomorrow, err := service.GetBoardEvents(ctx, "2026-03-03")
	if err != nil {
		t.Fatalf("board tomorrow: %v", err)
	}
	if len(tomorrow) != 1 || tomorrow[0].ID != future {
		t.Fatalf("tomorrow board = %#v, want only the scheduled future event", tomorrow)
	}

	month, err := service.GetEventsByDateRange(ctx, "2026-03-01", "2026-03-03")
	if err != nil {
		t.Fatalf("canonical month: %v", err)
	}
	if len(month) != 4 {
		t.Fatalf("canonical month = %d events, want 4: %#v", len(month), month)
	}
	monthByID := boardEventsByID(t, month)
	for _, id := range []int64{yTodo, yProgress, yDone, future} {
		if _, ok := monthByID[id]; !ok {
			t.Fatalf("canonical month is missing event %d: %#v", id, month)
		}
	}

	moved, err := service.SetBoardEventPhase(ctx, yTodo, PhaseDone, "2026-03-02")
	if err != nil {
		t.Fatalf("complete today: %v", err)
	}
	if moved.ID != yTodo || moved.Start != "2026-03-01T00:00:00" || moved.Phase != PhaseDone {
		t.Fatalf("phase mutation lost identity or schedule: %#v", moved)
	}
	if event := requireBoardEvent(t, mustBoard(t, service, "2026-03-01"), yTodo); event.Phase != PhaseTodo {
		t.Fatalf("completing today rewrote yesterday: %#v", event)
	}
	if event := requireBoardEvent(t, mustBoard(t, service, "2026-03-02"), yTodo); event.Phase != PhaseDone {
		t.Fatalf("today did not show completion: %#v", event)
	}
	for _, event := range mustBoard(t, service, "2026-03-03") {
		if event.ID == yTodo {
			t.Fatal("completed task carried into tomorrow")
		}
	}

	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM event_days WHERE event_id = ?`, yTodo).Scan(&rowCount); err != nil {
		t.Fatalf("count board rows: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := service.GetBoardEvents(ctx, "2026-03-02"); err != nil {
			t.Fatalf("repeat board read: %v", err)
		}
	}
	var repeatCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM event_days WHERE event_id = ?`, yTodo).Scan(&repeatCount); err != nil {
		t.Fatalf("count repeated board rows: %v", err)
	}
	if repeatCount != rowCount {
		t.Fatalf("repeated board reads wrote rows: before=%d after=%d", rowCount, repeatCount)
	}
}

func TestBoardPreservesDailyFieldsAcrossEditsAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "calendar.db")

	first := testServiceAt(t, path)
	firstNow := boardTestTime(2026, time.February, 28)
	first.now = func() time.Time { return firstNow }
	yesterdayResult := "yesterday result"
	yesterdayDeadline := "2026-03-10"
	created, err := first.CreateEvent(ctx, EventInput{
		Title:       "Carry me",
		Start:       "2026-02-28T14:00:00",
		End:         "2026-02-28T15:00:00",
		Color:       "#123456",
		AllDay:      false,
		Description: "yesterday details",
		Result:      &yesterdayResult,
		Bold:        true,
		Phase:       PhaseProgressing,
		Deadline:    &yesterdayDeadline,
	})
	if err != nil {
		t.Fatalf("create carried task: %v", err)
	}
	if err := first.db.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	second := testServiceAt(t, path)
	today := boardTestTime(2026, time.March, 1)
	second.now = func() time.Time { return today }
	carried := requireBoardEvent(t, mustBoard(t, second, "2026-03-01"), created.ID)
	if carried.Start != "2026-02-28T14:00:00" || carried.End != "2026-02-28T15:00:00" {
		t.Fatalf("carried task lost its timing: %#v", carried)
	}
	if carried.Description != "yesterday details" || carried.Result != "yesterday result" ||
		carried.Deadline != "2026-03-10" || carried.Color != "#123456" || !carried.Bold {
		t.Fatalf("carried task lost current fields: %#v", carried)
	}
	if carried.AllDay {
		t.Fatalf("carried task lost its timed schedule: %#v", carried)
	}

	todayResult := "today result"
	todayDeadline := "2026-03-11"
	updated, err := second.UpdateEvent(ctx, created.ID, EventInput{
		Title:       "Renamed today",
		Start:       created.Start,
		End:         created.End,
		Color:       "#abcdef",
		AllDay:      false,
		Description: "today details",
		Result:      &todayResult,
		Bold:        false,
		Deadline:    &todayDeadline,
		BoardDay:    "2026-03-01",
	})
	if err != nil {
		t.Fatalf("edit today's board: %v", err)
	}
	if updated.ID != created.ID || updated.Start != created.Start || updated.Phase != PhaseProgressing {
		t.Fatalf("board update lost identity or phase: %#v", updated)
	}
	if updated.Title != "Renamed today" || updated.Description != "today details" ||
		updated.Result != "today result" || updated.Deadline != "2026-03-11" || updated.Color != "#abcdef" {
		t.Fatalf("board update did not apply today's fields: %#v", updated)
	}

	yesterday := requireBoardEvent(t, mustBoard(t, second, "2026-02-28"), created.ID)
	if yesterday.Title != "Carry me" || yesterday.Description != "yesterday details" ||
		yesterday.Result != "yesterday result" || yesterday.Deadline != "2026-03-10" ||
		yesterday.Color != "#123456" || yesterday.Phase != PhaseProgressing {
		t.Fatalf("today's edit rewrote yesterday's record: %#v", yesterday)
	}

	done, err := second.SetBoardEventPhase(ctx, created.ID, PhaseDone, "2026-03-01")
	if err != nil {
		t.Fatalf("complete today: %v", err)
	}
	if done.Phase != PhaseDone || done.Start != created.Start {
		t.Fatalf("completion lost identity or schedule: %#v", done)
	}
	if yesterday := requireBoardEvent(t, mustBoard(t, second, "2026-02-28"), created.ID); yesterday.Phase != PhaseProgressing {
		t.Fatalf("completion rewrote yesterday's phase: %#v", yesterday)
	}
	if len(mustBoard(t, second, "2026-03-02")) != 0 {
		t.Fatal("completed task previewed on tomorrow's board")
	}
	if err := second.db.Close(); err != nil {
		t.Fatalf("close second database: %v", err)
	}

	third := testServiceAt(t, path)
	third.now = func() time.Time { return boardTestTime(2026, time.March, 2) }
	if len(mustBoard(t, third, "2026-03-02")) != 0 {
		t.Fatal("restart carried a completed task into tomorrow")
	}
	if event := requireBoardEvent(t, mustBoard(t, third, "2026-03-01"), created.ID); event.Phase != PhaseDone {
		t.Fatalf("restart lost today's completion: %#v", event)
	}
	if event := requireBoardEvent(t, mustBoard(t, third, "2026-02-28"), created.ID); event.Phase != PhaseProgressing {
		t.Fatalf("restart rewrote yesterday's phase: %#v", event)
	}
}

func TestBoardCarriesAcrossSkippedDaysAndYearBoundary(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "calendar.db")

	first := testServiceAt(t, path)
	first.now = func() time.Time { return boardTestTime(2026, time.December, 30) }
	created, err := first.CreateEvent(ctx, EventInput{
		Title:  "Year boundary task",
		Start:  "2026-12-30T00:00:00",
		End:    "2026-12-30T00:00:00",
		Color:  defaultEventColor,
		AllDay: true,
		Phase:  PhaseProgressing,
	})
	if err != nil {
		t.Fatalf("create boundary task: %v", err)
	}
	if err := first.db.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	second := testServiceAt(t, path)
	second.now = func() time.Time { return boardTestTime(2027, time.January, 2) }
	for _, day := range []string{"2026-12-31", "2027-01-01", "2027-01-02"} {
		if event := requireBoardEvent(t, mustBoard(t, second, day), created.ID); event.Phase != PhaseProgressing {
			t.Fatalf("skipped day %s lost the carried phase: %#v", day, event)
		}
	}
	if event := requireBoardEvent(t, mustBoard(t, second, "2026-12-30"), created.ID); event.Phase != PhaseProgressing {
		t.Fatalf("original day changed: %#v", event)
	}

	done, err := second.SetBoardEventPhase(ctx, created.ID, PhaseDone, "2027-01-02")
	if err != nil {
		t.Fatalf("complete boundary task: %v", err)
	}
	if done.Start != "2026-12-30T00:00:00" || done.Phase != PhaseDone {
		t.Fatalf("boundary completion lost identity or schedule: %#v", done)
	}
	if event := requireBoardEvent(t, mustBoard(t, second, "2027-01-01"), created.ID); event.Phase != PhaseProgressing {
		t.Fatalf("year boundary history was rewritten: %#v", event)
	}
	if len(mustBoard(t, second, "2027-01-03")) != 0 {
		t.Fatal("completed boundary task previewed on the next day")
	}
}

func TestEditingOlderBoardRecordKeepsLaterPhase(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	now := boardTestTime(2026, time.March, 1)
	service.now = func() time.Time { return now }

	created, err := service.CreateEvent(ctx, EventInput{
		Title:  "Older record",
		Start:  "2026-02-28T00:00:00",
		End:    "2026-02-28T00:00:00",
		Color:  defaultEventColor,
		AllDay: true,
		Phase:  PhaseProgressing,
	})
	if err != nil {
		t.Fatalf("create older task: %v", err)
	}
	if _, err := service.SetBoardEventPhase(ctx, created.ID, PhaseDone, "2026-03-01"); err != nil {
		t.Fatalf("complete today: %v", err)
	}

	if _, err := service.UpdateEvent(ctx, created.ID, EventInput{
		Title:       "Older record",
		Start:       created.Start,
		End:         created.End,
		Color:       created.Color,
		AllDay:      created.AllDay,
		Description: "yesterday edit",
		BoardDay:    "2026-02-28",
	}); err != nil {
		t.Fatalf("edit older record: %v", err)
	}
	canonical, err := eventByID(ctx, service.db, created.ID)
	if err != nil {
		t.Fatalf("read canonical event: %v", err)
	}
	if canonical.Phase != PhaseDone {
		t.Fatalf("editing an older record changed the canonical phase to %q", canonical.Phase)
	}
	if event := requireBoardEvent(t, mustBoard(t, service, "2026-03-01"), created.ID); event.Phase != PhaseDone {
		t.Fatalf("editing an older record changed today's phase: %#v", event)
	}
	yesterday := requireBoardEvent(t, mustBoard(t, service, "2026-02-28"), created.ID)
	if yesterday.Phase != PhaseProgressing || yesterday.Description != "yesterday edit" {
		t.Fatalf("older record did not keep its own daily state: %#v", yesterday)
	}

	if _, err := service.UpdateEvent(ctx, created.ID, EventInput{
		Title:       "Older record",
		Start:       created.Start,
		End:         created.End,
		Color:       created.Color,
		AllDay:      created.AllDay,
		Description: "yesterday edit",
		Phase:       PhaseTodo,
		BoardDay:    "2026-02-28",
	}); err != nil {
		t.Fatalf("rewind older phase: %v", err)
	}
	canonical, err = eventByID(ctx, service.db, created.ID)
	if err != nil {
		t.Fatalf("read canonical event after rewind: %v", err)
	}
	if canonical.Phase != PhaseDone {
		t.Fatalf("rewinding an older record overwrote the later phase: %q", canonical.Phase)
	}
	if event := requireBoardEvent(t, mustBoard(t, service, "2026-03-01"), created.ID); event.Phase != PhaseDone {
		t.Fatalf("rewinding an older record changed today's phase: %#v", event)
	}
	if event := requireBoardEvent(t, mustBoard(t, service, "2026-02-28"), created.ID); event.Phase != PhaseTodo {
		t.Fatalf("older record did not keep its explicit phase change: %#v", event)
	}
}

func TestReschedulingACarriedCardPreservesHistoryAndClosesTheGap(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	now := boardTestTime(2026, time.March, 1)
	service.now = func() time.Time { return now }
	card, err := service.CreateEvent(ctx, EventInput{
		Title: "Panel prep", Start: "2026-02-28T12:00:00", End: "2026-02-28T13:00:00", Phase: PhaseProgressing,
	})
	if err != nil {
		t.Fatal(err)
	}
	edit := eventTestInput(*card)
	edit.BoardDay = "2026-03-01"
	edit.Start, edit.End = "2026-03-04T14:00:00", "2026-03-04T15:00:00"
	if _, err = service.UpdateEvent(ctx, card.ID, edit); err != nil {
		t.Fatal(err)
	}
	if len(mustBoard(t, service, edit.BoardDay)) != 0 {
		t.Fatal("moving the schedule forward left a card on the old board")
	}
	if _, err := service.SetBoardEventPhase(ctx, card.ID, PhaseDone, edit.BoardDay); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale board move must not recreate a card: %v", err)
	}
	if old := requireBoardEvent(t, mustBoard(t, service, "2026-02-28"), card.ID); old.Start != card.Start || old.End != card.End {
		t.Fatalf("historical appointment times changed: %#v", old)
	}
	now = boardTestTime(2026, time.March, 5)
	if len(mustBoard(t, service, "2026-03-03")) != 0 {
		t.Fatal("carry leaked into the gap before the rescheduled date")
	}
	current := requireBoardEvent(t, mustBoard(t, service, "2026-03-05"), card.ID)
	if current.Start != edit.Start || current.Phase != PhaseProgressing {
		t.Fatalf("carry did not resume from the new schedule: %#v", current)
	}
}

func mustBoard(t *testing.T, service *Service, day string) []Event {
	t.Helper()
	events, err := service.GetBoardEvents(context.Background(), day)
	if err != nil {
		t.Fatalf("board %s: %v", day, err)
	}
	return events
}

func TestConsolidatedStepsAreHiddenOnlyFromTheirEffectiveDay(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	now := boardTestTime(2026, time.March, 1)
	service.now = func() time.Time { return now }
	create := func(title, start string) int64 {
		card, err := service.CreateEvent(ctx, EventInput{Title: title, Start: start + "T00:00:00", Phase: PhaseProgressing})
		if err != nil {
			t.Fatal(err)
		}
		return card.ID
	}
	pastStep := create("Earlier project detail", "2026-03-01")
	now = now.AddDate(0, 0, 1)
	today := now.Format(dateLayout)
	newStep := create("Today's project detail", today)
	summary := create("Deliver the project", today)
	for _, child := range []int64{pastStep, newStep} {
		if _, err := service.db.Exec("INSERT INTO board_consolidations(event_id,summary_id,from_day) VALUES(?,?,?)", child, summary, today); err != nil {
			t.Fatal(err)
		}
	}
	checkDay := func(day string, want ...int64) {
		t.Helper()
		cards, err := service.GetBoardEvents(ctx, day)
		if err != nil {
			t.Fatal(err)
		}
		if len(cards) != len(want) {
			t.Fatalf("day %s has %d cards, want %v", day, len(cards), want)
		}
		for i, id := range want {
			if cards[i].ID != id {
				t.Fatalf("day %s: wrong card %d", day, cards[i].ID)
			}
		}
	}
	checkDay("2026-03-01", pastStep)
	checkDay(today, summary)
	calendar, err := service.GetEventsByDateRange(ctx, "2026-03-01", today)
	if err != nil || len(calendar) != 2 || calendar[0].ID != pastStep || calendar[1].ID != summary {
		t.Fatalf("month exposed a folded step: %+v, %v", calendar, err)
	}
	plans, err := service.GetChatPlanningEvents(ctx, today)
	if err != nil || len(plans) != 1 || plans[0].ID != summary {
		t.Fatalf("planner exposed a folded step: %+v, %v", plans, err)
	}
	if err := service.DeleteEvent(ctx, summary); err != nil {
		t.Fatal(err)
	}
	checkDay(today)
	checkDay("2026-03-01", pastStep)
}
