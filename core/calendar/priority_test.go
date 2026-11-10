package calendar

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

func priorityPhaseOrder(events []Event, phase string) []int64 {
	order := make([]int64, 0, len(events))
	for _, event := range events {
		if event.Phase == phase {
			order = append(order, event.ID)
		}
	}
	return order
}

func priorityRequireOrder(t *testing.T, service *Service, day, phase string, want ...int64) {
	t.Helper()
	got := priorityPhaseOrder(mustBoard(t, service, day), phase)
	if !slices.Equal(got, want) {
		t.Fatalf("%s %s order = %v, want %v", day, phase, got, want)
	}
}

func priorityRequireRows(t *testing.T, service *Service, day, phase string, want ...int64) {
	t.Helper()
	rows, err := service.db.Query(
		"SELECT event_id FROM board_orders WHERE day = ? AND phase = ? ORDER BY position", day, phase)
	if err != nil {
		t.Fatalf("read board order rows: %v", err)
	}
	defer rows.Close()
	got := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan board order row: %v", err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read board order rows: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s %s saved order = %v, want %v", day, phase, got, want)
	}
}

func priorityOrderRowCount(t *testing.T, service *Service) int {
	t.Helper()
	var count int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM board_orders").Scan(&count); err != nil {
		t.Fatalf("count board order rows: %v", err)
	}
	return count
}

func priorityRequireEventUnchanged(t *testing.T, service *Service, before Event) {
	t.Helper()
	after, err := eventByID(context.Background(), service.db, before.ID)
	if err != nil {
		t.Fatalf("read event %d after reorder: %v", before.ID, err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("reorder edited event %d: before=%#v after=%#v", before.ID, before, after)
	}
}

func TestReorderBoardEventMovesTopBottomAndMiddleWithinColumn(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := now.Format(dateLayout)

	a := insertBoardTestEvent(t, service.db, "Alpha", day+"T09:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	b := insertBoardTestEvent(t, service.db, "Bravo", day+"T10:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	c := insertBoardTestEvent(t, service.db, "Charlie", day+"T11:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	d := insertBoardTestEvent(t, service.db, "Delta", day+"T12:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	other := insertBoardTestEvent(t, service.db, "Other phase", day+"T08:00:00", PhaseProgressing, "", "", "", "2026-03-02 08:00:00")

	before := make(map[int64]Event)
	for _, id := range []int64{a, b, c, d, other} {
		event, err := eventByID(ctx, service.db, id)
		if err != nil {
			t.Fatalf("read event %d: %v", id, err)
		}
		before[id] = event
	}
	var historyRows int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM event_days").Scan(&historyRows); err != nil {
		t.Fatalf("count board history: %v", err)
	}

	priorityRequireOrder(t, service, day, PhaseTodo, a, b, c, d)
	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, d, a); err != nil {
		t.Fatalf("move d before a: %v", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, d, a, b, c)
	priorityRequireOrder(t, service, day, PhaseProgressing, other)
	priorityRequireRows(t, service, day, PhaseTodo, d, a, b, c)

	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, d, 0); err != nil {
		t.Fatalf("move d to bottom: %v", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, a, b, c, d)

	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, b, d); err != nil {
		t.Fatalf("move b before d: %v", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, a, c, b, d)
	priorityRequireRows(t, service, day, PhaseTodo, a, c, b, d)
	priorityRequireOrder(t, service, day, PhaseProgressing, other)
	priorityRequireRows(t, service, day, PhaseProgressing)

	for _, event := range before {
		priorityRequireEventUnchanged(t, service, event)
	}
	var afterHistoryRows int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM event_days").Scan(&afterHistoryRows); err != nil {
		t.Fatalf("count board history after reorder: %v", err)
	}
	if afterHistoryRows != historyRows {
		t.Fatalf("reorder wrote daily history: before=%d after=%d", historyRows, afterHistoryRows)
	}
}

func TestReorderBoardEventNoOpAndStaleActionsDoNotWrite(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := now.Format(dateLayout)

	a := insertBoardTestEvent(t, service.db, "Alpha", day+"T09:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	b := insertBoardTestEvent(t, service.db, "Bravo", day+"T10:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	c := insertBoardTestEvent(t, service.db, "Charlie", day+"T11:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	other := insertBoardTestEvent(t, service.db, "Other phase", day+"T08:00:00", PhaseProgressing, "", "", "", "2026-03-02 08:00:00")
	deleted := insertBoardTestEvent(t, service.db, "Deleted", day+"T07:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	if err := service.DeleteEvent(ctx, deleted); err != nil {
		t.Fatalf("delete anchor fixture: %v", err)
	}

	for name, call := range map[string]func() error{
		"drop before self":       func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, a, a) },
		"drop before next":       func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, a, b) },
		"append while last":      func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, c, 0) },
		"other-phase anchor":     func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, a, other) },
		"other-phase moved card": func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, other, b) },
		"deleted anchor":         func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, c, deleted) },
		"missing anchor":         func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, c, 999999) },
		"missing moved card":     func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, 999999, a) },
		"zero moved id":          func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, 0, a) },
		"negative anchor id":     func() error { return service.ReorderBoardEvent(ctx, day, PhaseTodo, a, -1) },
		"invalid phase":          func() error { return service.ReorderBoardEvent(ctx, day, "blocked", a, b) },
		"invalid day":            func() error { return service.ReorderBoardEvent(ctx, "2026-03-02T00:00:00", PhaseTodo, a, b) },
	} {
		if name == "drop before self" || name == "drop before next" || name == "append while last" {
			if err := call(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		} else if err := call(); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if count := priorityOrderRowCount(t, service); count != 0 {
			t.Fatalf("%s wrote %d order rows", name, count)
		}
	}

	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, c, a); err != nil {
		t.Fatalf("move c before a: %v", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, c, a, b)
	priorityRequireRows(t, service, day, PhaseTodo, c, a, b)

	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, c, a); err != nil {
		t.Fatalf("repeat the same move: %v", err)
	}
	priorityRequireRows(t, service, day, PhaseTodo, c, a, b)

	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, a, deleted); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale anchor error = %v, want sql.ErrNoRows", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, c, a, b)
	priorityRequireRows(t, service, day, PhaseTodo, c, a, b)

	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, a, other); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other-phase anchor error = %v, want sql.ErrNoRows", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, c, a, b)
	priorityRequireRows(t, service, day, PhaseTodo, c, a, b)

	if err := service.DeleteEvent(ctx, b); err != nil {
		t.Fatalf("delete ranked card: %v", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, c, a)
	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, a, b); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted moved card error = %v, want sql.ErrNoRows", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, c, a)
	priorityRequireRows(t, service, day, PhaseTodo, c, a)
}

func TestBoardUsesChronologicalFallbackUntilFirstReorder(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	day := now.Format(dateLayout)

	late := insertBoardTestEvent(t, service.db, "Late", day+"T10:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	first := insertBoardTestEvent(t, service.db, "First", day+"T09:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")
	tie := insertBoardTestEvent(t, service.db, "Tie", day+"T09:00:00", PhaseTodo, "", "", "", "2026-03-02 08:00:00")

	priorityRequireOrder(t, service, day, PhaseTodo, first, tie, late)
	if count := priorityOrderRowCount(t, service); count != 0 {
		t.Fatalf("unranked board wrote %d order rows", count)
	}
}

func TestBoardOrderCarriesForwardAndKeepsEarlierAndLaterSnapshots(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	firstDay := now.Format(dateLayout)

	create := func(title, start string) int64 {
		t.Helper()
		event, err := service.CreateEvent(ctx, EventInput{
			Title: title, Start: start, Color: defaultEventColor, Phase: PhaseTodo,
		})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return event.ID
	}
	a := create("Alpha", firstDay+"T09:00:00")
	b := create("Bravo", firstDay+"T10:00:00")
	c := create("Charlie", firstDay+"T11:00:00")

	if err := service.ReorderBoardEvent(ctx, firstDay, PhaseTodo, c, a); err != nil {
		t.Fatalf("reorder first day: %v", err)
	}
	secondDay := "2026-03-03"
	thirdDay := "2026-03-04"
	priorityRequireOrder(t, service, firstDay, PhaseTodo, c, a, b)

	now = boardTestTime(2026, time.March, 3)
	priorityRequireOrder(t, service, secondDay, PhaseTodo, c, a, b)
	if err := service.ReorderBoardEvent(ctx, secondDay, PhaseTodo, b, c); err != nil {
		t.Fatalf("reorder second day: %v", err)
	}
	priorityRequireOrder(t, service, firstDay, PhaseTodo, c, a, b)
	priorityRequireOrder(t, service, secondDay, PhaseTodo, b, c, a)

	now = boardTestTime(2026, time.March, 4)
	priorityRequireOrder(t, service, thirdDay, PhaseTodo, b, c, a)

	if err := service.ReorderBoardEvent(ctx, firstDay, PhaseTodo, c, 0); err != nil {
		t.Fatalf("reorder first day after second snapshot: %v", err)
	}
	priorityRequireOrder(t, service, firstDay, PhaseTodo, a, b, c)
	priorityRequireOrder(t, service, secondDay, PhaseTodo, b, c, a)
	priorityRequireOrder(t, service, thirdDay, PhaseTodo, b, c, a)
}

func TestBoardOrderIsIndependentPerPhaseAndNewCardsFollowRankedCards(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := now.Format(dateLayout)

	create := func(title, start string, phase string) int64 {
		t.Helper()
		event, err := service.CreateEvent(ctx, EventInput{
			Title: title, Start: start, Color: defaultEventColor, Phase: phase,
		})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return event.ID
	}
	a := create("Alpha", day+"T09:00:00", PhaseTodo)
	b := create("Bravo", day+"T10:00:00", PhaseTodo)
	c := create("Charlie", day+"T11:00:00", PhaseTodo)
	p := create("Papa", day+"T09:00:00", PhaseProgressing)
	q := create("Quebec", day+"T10:00:00", PhaseProgressing)

	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, c, a); err != nil {
		t.Fatalf("reorder todo column: %v", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, c, a, b)
	priorityRequireOrder(t, service, day, PhaseProgressing, p, q)
	priorityRequireRows(t, service, day, PhaseProgressing)

	newTodo := create("New earlier todo", day+"T07:00:00", PhaseTodo)
	priorityRequireOrder(t, service, day, PhaseTodo, c, a, b, newTodo)

	newProgress := create("New earlier progress", day+"T07:00:00", PhaseProgressing)
	priorityRequireOrder(t, service, day, PhaseProgressing, newProgress, p, q)

	if err := service.ReorderBoardEvent(ctx, day, PhaseTodo, newTodo, a); err != nil {
		t.Fatalf("rank new todo before a: %v", err)
	}
	priorityRequireOrder(t, service, day, PhaseTodo, c, newTodo, a, b)
	priorityRequireOrder(t, service, day, PhaseProgressing, newProgress, p, q)

	if err := service.ReorderBoardEvent(ctx, day, PhaseProgressing, q, p); err != nil {
		t.Fatalf("rank progressing column: %v", err)
	}
	priorityRequireOrder(t, service, day, PhaseProgressing, newProgress, q, p)
	priorityRequireOrder(t, service, day, PhaseTodo, c, newTodo, a, b)
}

func TestReorderBoardEventPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "calendar.db")
	day := "2026-03-02"

	first := testServiceAt(t, path)
	first.now = func() time.Time { return boardTestTime(2026, time.March, 2) }
	ids := make([]int64, 0, 3)
	starts := []string{day + "T09:00:00", day + "T10:00:00", day + "T11:00:00"}
	for index, title := range []string{"Alpha", "Bravo", "Charlie"} {
		event, err := first.CreateEvent(ctx, EventInput{
			Title: title, Start: starts[index],
			Color: defaultEventColor, Phase: PhaseTodo,
		})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		ids = append(ids, event.ID)
	}
	if err := first.ReorderBoardEvent(ctx, day, PhaseTodo, ids[2], ids[0]); err != nil {
		t.Fatalf("reorder before reopening: %v", err)
	}
	if err := first.db.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	second := testServiceAt(t, path)
	second.now = func() time.Time { return boardTestTime(2026, time.March, 2) }
	priorityRequireOrder(t, second, day, PhaseTodo, ids[2], ids[0], ids[1])
	second.now = func() time.Time { return boardTestTime(2026, time.March, 3) }
	priorityRequireOrder(t, second, "2026-03-03", PhaseTodo, ids[2], ids[0], ids[1])
}
