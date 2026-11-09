package calendar

import (
	"context"
	"testing"
	"time"
)

func TestWorkspacesAreSeededAndValidated(t *testing.T) {
	service := testService(t)
	ctx := context.Background()

	workspaces, err := service.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	if len(workspaces) != 3 {
		t.Fatalf("workspaces = %#v, want the three seeded projects", workspaces)
	}
	for id, want := range map[int64]string{
		1: "Daily Routine",
		2: "Seekino",
		3: "个人 IP 打造",
	} {
		if workspaces[id-1].ID != id || workspaces[id-1].Name != want {
			t.Fatalf("workspace %d = %#v, want %q", id, workspaces[id-1], want)
		}
	}

	created, err := service.CreateWorkspace(ctx, "  Focus  ", "  deep work  ")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if created.Name != "Focus" || created.Hint != "deep work" {
		t.Fatalf("created workspace = %#v, want trimmed fields", created)
	}
	if _, err := service.CreateWorkspace(ctx, "focus", ""); err == nil {
		t.Fatal("duplicate workspace name was accepted")
	}
	if _, err := service.CreateWorkspace(ctx, " All ", ""); err == nil {
		t.Fatal("reserved virtual view name was accepted")
	}
	if _, err := service.CreateWorkspace(ctx, "   ", ""); err == nil {
		t.Fatal("blank workspace name was accepted")
	}

	if _, err := service.UpdateWorkspace(ctx, 2, "Seekino Lab", "interviews"); err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	if _, err := service.UpdateWorkspace(ctx, 1, "Renamed routine", ""); err == nil {
		t.Fatal("Daily Routine was allowed to be renamed")
	}
	updatedRoutine, err := service.UpdateWorkspace(ctx, 1, "Daily Routine", "morning routine")
	if err != nil {
		t.Fatalf("update routine hint: %v", err)
	}
	if updatedRoutine.Name != "Daily Routine" || updatedRoutine.Hint != "morning routine" {
		t.Fatalf("routine workspace = %#v", updatedRoutine)
	}
	if _, err := service.UpdateWorkspace(ctx, 999, "Ghost", ""); err == nil {
		t.Fatal("unknown workspace was updated")
	}
}

func TestEventsDefaultToRoutineAndMoveWorkspaceWithHistory(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()

	created, err := service.CreateEvent(ctx, EventInput{
		Title: "Default project task",
		Start: "2026-03-01T00:00:00",
		End:   "2026-03-01T00:00:00",
		Color: defaultEventColor,
		Phase: PhaseProgressing,
	})
	if err != nil {
		t.Fatalf("create default workspace event: %v", err)
	}
	if created.WorkspaceID != RoutineWorkspaceID {
		t.Fatalf("created workspace = %d, want Daily Routine", created.WorkspaceID)
	}

	input := eventTestInput(*created)
	input.BoardDay = "2026-03-02"
	input.WorkspaceID = 2
	moved, err := service.UpdateEvent(ctx, created.ID, input)
	if err != nil {
		t.Fatalf("move workspace: %v", err)
	}
	if moved.ID != created.ID || moved.WorkspaceID != 2 {
		t.Fatalf("moved event = %#v, want stable id in Seekino", moved)
	}
	canonical, err := eventByID(ctx, service.db, created.ID)
	if err != nil {
		t.Fatalf("read canonical event: %v", err)
	}
	if canonical.WorkspaceID != 2 {
		t.Fatalf("canonical workspace = %d, want 2", canonical.WorkspaceID)
	}
	if yesterday := requireBoardEvent(t, mustBoard(t, service, "2026-03-01"), created.ID); yesterday.WorkspaceID != RoutineWorkspaceID {
		t.Fatalf("yesterday's project changed to %d", yesterday.WorkspaceID)
	}
	if today := requireBoardEvent(t, mustBoard(t, service, "2026-03-02"), created.ID); today.WorkspaceID != 2 {
		t.Fatalf("today's project = %d, want 2", today.WorkspaceID)
	}
	month, err := service.GetEventsByDateRange(ctx, "2026-03-01", "2026-03-02")
	if err != nil || len(month) != 1 || month[0].WorkspaceID != 2 {
		t.Fatalf("month row workspace = %#v, err=%v", month, err)
	}

	if _, err := service.CreateEvent(ctx, EventInput{
		Title:       "Unknown project",
		Start:       "2026-03-02T00:00:00",
		WorkspaceID: 99,
	}); err == nil {
		t.Fatal("event was created in an unknown workspace")
	}
	if _, err := service.UpdateEvent(ctx, created.ID, EventInput{
		Title:       "Unknown project",
		Start:       created.Start,
		WorkspaceID: 99,
	}); err == nil {
		t.Fatal("event was moved to an unknown workspace")
	}
}

func TestEmailBatchCreatesInRoutineWorkspace(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	source := EmailSource{
		AccountID: "primary", MessageID: "mail@example.com", URL: "catendar://email/primary?uid=1",
	}
	result, err := service.BatchCreateFromEmail(ctx, []EmailEventInput{{
		Title:         "Mail task",
		Start:         "2026-03-02T09:00:00",
		End:           "2026-03-02T10:00:00",
		SourceEmailID: "mail",
	}}, map[string]EmailSource{"mail": source})
	if err != nil || result.Created != 1 {
		t.Fatalf("email batch = %#v err=%v", result, err)
	}
	events, err := service.GetEventsByDateRange(ctx, "2026-03-02", "2026-03-02")
	if err != nil || len(events) != 1 || events[0].WorkspaceID != RoutineWorkspaceID {
		t.Fatalf("email event = %#v err=%v", events, err)
	}
}

func TestSameTitleLivesInSeparateWorkspacesAcrossViews(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()

	first, err := service.CreateEvent(ctx, EventInput{
		Title: "Weekly review", Start: "2026-03-02T00:00:00", Color: defaultEventColor, WorkspaceID: 2,
	})
	if err != nil {
		t.Fatalf("create Seekino review: %v", err)
	}
	second, err := service.CreateEvent(ctx, EventInput{
		Title: "Weekly review", Start: "2026-03-02T00:00:00", Color: defaultEventColor, WorkspaceID: 3,
	})
	if err != nil {
		t.Fatalf("create personal review: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("same title in different workspaces collapsed into one task")
	}
	board := mustBoard(t, service, "2026-03-02")
	if len(board) != 2 {
		t.Fatalf("board = %#v, want both workspaces", board)
	}
	byWorkspace := make(map[int64]int64, len(board))
	for _, event := range board {
		byWorkspace[event.WorkspaceID] = event.ID
	}
	if byWorkspace[2] != first.ID || byWorkspace[3] != second.ID {
		t.Fatalf("board workspace identities = %#v", byWorkspace)
	}
	month, err := service.GetEventsByDateRange(ctx, "2026-03-02", "2026-03-02")
	if err != nil || len(month) != 2 {
		t.Fatalf("month = %#v, err=%v", month, err)
	}
	for _, event := range month {
		if event.ID != byWorkspace[event.WorkspaceID] {
			t.Fatalf("month and board disagree on task %d", event.ID)
		}
	}
}
