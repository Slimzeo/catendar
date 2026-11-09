package calendar

import (
	"context"
	"strings"
	"testing"
	"time"
)

func chatTestEvidence(id, threadID, title string, at time.Time) ChatEvidence {
	return ChatEvidence{ID: id, ThreadID: threadID, ThreadTitle: title, At: at.UnixMilli(), Quote: "synthetic evidence"}
}

func pendingChatActivity(t *testing.T, service *Service) ChatActivity {
	t.Helper()
	activities, err := service.ListChatActivity(context.Background())
	if err != nil {
		t.Fatalf("list chat activity: %v", err)
	}
	for _, activity := range activities {
		if activity.State == chatStatePending {
			return activity
		}
	}
	t.Fatalf("no pending chat activity in %#v", activities)
	return ChatActivity{}
}

func TestApplyChatPlanCreatesOnceAndMarksSourcesSeen(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	change := ChatChange{
		Source:      chatTestEvidence("msg-1", "thread-1", "Board planning", now),
		TaskKey:     "ship-board",
		WorkspaceID: 2,
		Title:       "Ship the board",
		Phase:       PhaseProgressing,
		Description: "from Codex",
	}
	first, err := service.ApplyChatPlan(ctx, day, []string{"msg-1"}, []ChatChange{change})
	if err != nil {
		t.Fatalf("first chat plan: %v", err)
	}
	if first.Created != 1 || first.Updated != 0 || first.Pending != 0 || first.Skipped != 0 {
		t.Fatalf("first chat counts = %#v", first)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("events after chat plan = %#v, err=%v", events, err)
	}
	if events[0].WorkspaceID != 2 || events[0].Phase != PhaseProgressing {
		t.Fatalf("created chat task = %#v", events[0])
	}
	createdID := events[0].ID

	unseen, err := service.UnseenChatSources(ctx, []string{"msg-1", "msg-2"})
	if err != nil {
		t.Fatalf("unseen sources: %v", err)
	}
	if unseen["msg-1"] || !unseen["msg-2"] || len(unseen) != 1 {
		t.Fatalf("unseen sources = %#v", unseen)
	}

	second, err := service.ApplyChatPlan(ctx, day, []string{"msg-1"}, []ChatChange{change})
	if err != nil {
		t.Fatalf("second chat plan: %v", err)
	}
	if second.Created != 0 || second.Updated != 0 || second.Pending != 0 || second.Skipped != 1 {
		t.Fatalf("second chat counts = %#v", second)
	}
	after, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(after) != 1 || after[0].ID != createdID {
		t.Fatalf("repeat plan changed events = %#v, err=%v", after, err)
	}
}

func TestApplyChatPlanSupportsSameTitleInSeparateWorkspaces(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	changes := []ChatChange{
		{
			Source:      chatTestEvidence("msg-1", "thread-1", "Work", now),
			TaskKey:     "weekly-work",
			WorkspaceID: 2,
			Title:       "Weekly review",
			Phase:       PhaseTodo,
		},
		{
			Source:      chatTestEvidence("msg-2", "thread-2", "Personal", now),
			TaskKey:     "weekly-personal",
			WorkspaceID: 3,
			Title:       "Weekly review",
			Phase:       PhaseTodo,
		},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-1", "msg-2"}, changes)
	if err != nil {
		t.Fatalf("apply chat plan: %v", err)
	}
	if counts.Created != 2 || counts.Skipped != 0 {
		t.Fatalf("chat counts = %#v", counts)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, err=%v", events, err)
	}
	workspaces := map[int64]int64{}
	for _, event := range events {
		workspaces[event.WorkspaceID] = event.ID
	}
	if workspaces[2] == 0 || workspaces[3] == 0 || workspaces[2] == workspaces[3] {
		t.Fatalf("workspace identities = %#v", workspaces)
	}
}

func TestChatConflictQueuesPendingAndDismissalBlocksReplay(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	created, err := service.CreateEvent(ctx, EventInput{
		Title:       "Manual task",
		Start:       day + "T00:00:00",
		Color:       defaultEventColor,
		Description: "human text",
		Phase:       PhaseProgressing,
	})
	if err != nil {
		t.Fatalf("create manual task: %v", err)
	}
	change := ChatChange{
		Source:      chatTestEvidence("msg-conflict", "thread-1", "Planning", now),
		TaskKey:     "manual-task",
		EventID:     created.ID,
		WorkspaceID: 1,
		Title:       "Manual task",
		Phase:       PhaseProgressing,
		Description: "AI text",
		BaseUpdated: created.UpdatedAt.Format(time.RFC3339Nano),
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-conflict"}, []ChatChange{change})
	if err != nil {
		t.Fatalf("conflicting chat plan: %v", err)
	}
	if counts.Pending != 1 || counts.Updated != 0 || counts.Created != 0 {
		t.Fatalf("conflict counts = %#v", counts)
	}
	canonical, err := eventByID(ctx, service.db, created.ID)
	if err != nil || canonical.Description != "human text" {
		t.Fatalf("conflict overwrote the task: %#v err=%v", canonical, err)
	}

	pending := pendingChatActivity(t, service)
	if pending.BeforePhase != PhaseProgressing || !strings.Contains(pending.Reason, "description") {
		t.Fatalf("pending activity = %#v", pending)
	}
	dismissed, err := service.ResolveChatChange(ctx, pending.ID, false)
	if err != nil {
		t.Fatalf("dismiss pending change: %v", err)
	}
	if dismissed.State != chatStateDismissed {
		t.Fatalf("dismissed state = %q", dismissed.State)
	}

	replay, err := service.ApplyChatPlan(ctx, day, []string{"msg-conflict"}, []ChatChange{change})
	if err != nil {
		t.Fatalf("replay dismissed source: %v", err)
	}
	if replay.Skipped != 1 || replay.Created != 0 || replay.Updated != 0 || replay.Pending != 0 {
		t.Fatalf("replay counts = %#v", replay)
	}
	canonical, err = eventByID(ctx, service.db, created.ID)
	if err != nil || canonical.Description != "human text" {
		t.Fatalf("dismissed source resurrected the change: %#v err=%v", canonical, err)
	}
}

func TestChatApprovalAppliesPendingChangeIdempotently(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	created, err := service.CreateEvent(ctx, EventInput{
		Title: "Reviewed task", Start: day + "T00:00:00", Color: defaultEventColor,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	change := ChatChange{
		Source:      chatTestEvidence("msg-review", "thread-1", "Review", now),
		TaskKey:     "reviewed-task",
		EventID:     created.ID,
		WorkspaceID: 1,
		Title:       "Reviewed task",
		Phase:       PhaseProgressing,
		Description: "approved description",
		NeedsReview: true,
		BaseUpdated: created.UpdatedAt.Format(time.RFC3339Nano),
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-review"}, []ChatChange{change})
	if err != nil || counts.Pending != 1 {
		t.Fatalf("queue review: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	approved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil {
		t.Fatalf("approve change: %v", err)
	}
	if approved.State != chatStateApplied || approved.Change.EventID != created.ID {
		t.Fatalf("approved activity = %#v", approved)
	}
	canonical, err := eventByID(ctx, service.db, created.ID)
	if err != nil || canonical.Phase != PhaseProgressing || canonical.Description != "approved description" {
		t.Fatalf("approved task = %#v err=%v", canonical, err)
	}

	again, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil {
		t.Fatalf("repeat approval: %v", err)
	}
	if again.State != chatStateApplied {
		t.Fatalf("repeat approval state = %q", again.State)
	}
	var eventCount int
	if err := service.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("event count after repeat approval = %d err=%v", eventCount, err)
	}
}

func TestChatStaleAndDeletedTargetsAreRejectedWithoutClobbering(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	created, err := service.CreateEvent(ctx, EventInput{
		Title: "Protected task", Start: day + "T00:00:00", Color: defaultEventColor,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	change := ChatChange{
		Source:      chatTestEvidence("msg-stale", "thread-1", "Planning", now),
		TaskKey:     "protected-task",
		EventID:     created.ID,
		Title:       "Protected task",
		Phase:       PhaseProgressing,
		NeedsReview: true,
		BaseUpdated: created.UpdatedAt.Format(time.RFC3339Nano),
	}
	if counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-stale"}, []ChatChange{change}); err != nil || counts.Pending != 1 {
		t.Fatalf("queue stale review: counts=%#v err=%v", counts, err)
	}
	now = now.Add(time.Hour)
	if _, err := service.SetEventPhase(ctx, created.ID, PhaseDone); err != nil {
		t.Fatalf("manual edit: %v", err)
	}
	pending := pendingChatActivity(t, service)
	resolved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil {
		t.Fatalf("resolve stale change: %v", err)
	}
	if resolved.State != chatStateDismissed || !strings.Contains(resolved.Reason, "changed") {
		t.Fatalf("stale resolution = %#v", resolved)
	}
	canonical, err := eventByID(ctx, service.db, created.ID)
	if err != nil || canonical.Phase != PhaseDone {
		t.Fatalf("stale approval clobbered manual edit: %#v err=%v", canonical, err)
	}
}

func TestChatAutomaticDoneRequiresResult(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	created, err := service.CreateEvent(ctx, EventInput{
		Title: "Needs result", Start: day + "T00:00:00", Color: defaultEventColor,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	change := ChatChange{
		Source:      chatTestEvidence("msg-done", "thread-1", "Done", now),
		TaskKey:     "needs-result",
		EventID:     created.ID,
		Title:       "Needs result",
		Phase:       PhaseDone,
		BaseUpdated: created.UpdatedAt.Format(time.RFC3339Nano),
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-done"}, []ChatChange{change})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("done without result: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	resolved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil || resolved.State != chatStateDismissed {
		t.Fatalf("done approval = %#v err=%v", resolved, err)
	}
	canonical, err := eventByID(ctx, service.db, created.ID)
	if err != nil || canonical.Phase != PhaseTodo {
		t.Fatalf("task completed without a result: %#v err=%v", canonical, err)
	}
}

func TestChatPlanRejectsWrongDayWithoutMarkingSources(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }

	if _, err := service.ApplyChatPlan(context.Background(), "2026-03-01", []string{"msg-old"}, nil); err == nil {
		t.Fatal("wrong-day chat plan was accepted")
	}
	unseen, err := service.UnseenChatSources(context.Background(), []string{"msg-old"})
	if err != nil {
		t.Fatalf("unseen sources: %v", err)
	}
	if !unseen["msg-old"] {
		t.Fatal("rejected plan marked its sources as processed")
	}
}

func TestChatNeverRecreatesADeletedLinkedTask(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	first := ChatChange{
		Source:  chatTestEvidence("msg-1", "thread-1", "Planning", now),
		TaskKey: "stable-task",
		Title:   "Stable task",
		Phase:   PhaseTodo,
	}
	if counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-1"}, []ChatChange{first}); err != nil || counts.Created != 1 {
		t.Fatalf("create linked task: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("created events = %#v err=%v", events, err)
	}
	if err := service.DeleteEvent(ctx, events[0].ID); err != nil {
		t.Fatalf("delete linked task: %v", err)
	}

	repeat := ChatChange{
		Source:  chatTestEvidence("msg-2", "thread-1", "More planning", now),
		TaskKey: "stable-task",
		Title:   "Stable task",
		Phase:   PhaseTodo,
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-2"}, []ChatChange{repeat})
	if err != nil {
		t.Fatalf("repeat evidence: %v", err)
	}
	if counts.Created != 0 || counts.Updated != 0 || counts.Skipped != 1 {
		t.Fatalf("repeat evidence counts = %#v", counts)
	}
	after, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(after) != 0 {
		t.Fatalf("deleted task was recreated: %#v err=%v", after, err)
	}
}

func TestApplyChatPlanRejectsUnauthorizedOrIncompleteEvidenceAtomically(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	valid := ChatChange{
		Source:  chatTestEvidence("msg-valid", "thread-1", "Planning", now),
		TaskKey: "valid-task",
		Title:   "Valid task",
	}
	outsider := ChatChange{
		Source:  chatTestEvidence("msg-outsider", "thread-1", "Planning", now),
		TaskKey: "outsider-task",
		Title:   "Outsider task",
	}
	if _, err := service.ApplyChatPlan(ctx, day, []string{"msg-valid"}, []ChatChange{valid, outsider}); err == nil {
		t.Fatal("plan with an unauthorized proposal source was accepted")
	}
	for _, id := range []string{"msg-valid", "msg-outsider"} {
		unseen, err := service.UnseenChatSources(ctx, []string{id})
		if err != nil || !unseen[id] {
			t.Fatalf("source %q was marked seen after a rejected plan: %#v err=%v", id, unseen, err)
		}
	}
	if events, err := service.GetEventsByDateRange(ctx, day, day); err != nil || len(events) != 0 {
		t.Fatalf("rejected plan wrote events: %#v err=%v", events, err)
	}

	for _, tc := range []struct {
		name   string
		source ChatEvidence
	}{
		{"missing thread", ChatEvidence{ID: "msg-no-thread", Quote: "quoted", At: now.UnixMilli()}},
		{"missing quote", ChatEvidence{ID: "msg-no-quote", ThreadID: "thread-1", At: now.UnixMilli()}},
		{"missing timestamp", ChatEvidence{ID: "msg-no-time", ThreadID: "thread-1", Quote: "quoted"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			change := ChatChange{Source: tc.source, TaskKey: "evidence-task", Title: "Evidence task"}
			if _, err := service.ApplyChatPlan(ctx, day, []string{tc.source.ID}, []ChatChange{change}); err == nil {
				t.Fatalf("plan with %s was accepted", tc.name)
			}
			unseen, err := service.UnseenChatSources(ctx, []string{tc.source.ID})
			if err != nil || !unseen[tc.source.ID] {
				t.Fatalf("source %q was marked seen: %#v err=%v", tc.source.ID, unseen, err)
			}
		})
	}
	if events, err := service.GetEventsByDateRange(ctx, day, day); err != nil || len(events) != 0 {
		t.Fatalf("invalid evidence wrote events: %#v err=%v", events, err)
	}
}

func TestChatTitleFallbackIgnoresHistoricalAndFutureCards(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	historical, err := service.CreateEvent(ctx, EventInput{
		Title: "Weekly  Review", Start: "2026-03-01T00:00:00", Color: defaultEventColor,
		Phase: PhaseDone,
	})
	if err != nil {
		t.Fatalf("create historical task: %v", err)
	}
	future, err := service.CreateEvent(ctx, EventInput{
		Title: "Weekly Review", Start: "2026-03-03T00:00:00", Color: defaultEventColor,
	})
	if err != nil {
		t.Fatalf("create future task: %v", err)
	}

	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-title"}, []ChatChange{{
		Source:      chatTestEvidence("msg-title", "thread-1", "Planning", now),
		TaskKey:     "weekly-review",
		WorkspaceID: 1,
		Title:       "weekly review",
		Phase:       PhaseTodo,
	}})
	if err != nil {
		t.Fatalf("apply title plan: %v", err)
	}
	if counts.Created != 1 || counts.Updated != 0 || counts.Pending != 0 {
		t.Fatalf("title fallback counts = %#v", counts)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("events after title plan = %#v err=%v", events, err)
	}
	if events[0].ID == historical.ID || events[0].ID == future.ID {
		t.Fatalf("title fallback retargeted an unrelated card: %#v", events[0])
	}
	if old, err := eventByID(ctx, service.db, historical.ID); err != nil || old.Phase != PhaseDone || old.Title != "Weekly  Review" {
		t.Fatalf("historical card changed: %#v err=%v", old, err)
	}
	if planned, err := eventByID(ctx, service.db, future.ID); err != nil || planned.Start != "2026-03-03T00:00:00" {
		t.Fatalf("future card changed: %#v err=%v", planned, err)
	}
}

func TestChatTitleFallbackMatchesCarriedOpenCard(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	carried, err := service.CreateEvent(ctx, EventInput{
		Title: "Carry review", Start: "2026-03-01T00:00:00", Color: defaultEventColor,
		Phase: PhaseProgressing, Description: "carried description",
	})
	if err != nil {
		t.Fatalf("create carried task: %v", err)
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-carried"}, []ChatChange{{
		Source:      chatTestEvidence("msg-carried", "thread-1", "Planning", now),
		TaskKey:     "carry-review",
		WorkspaceID: 1,
		Title:       "Carry review",
		Phase:       PhaseProgressing,
	}})
	if err != nil || counts.Updated != 1 || counts.Created != 0 || counts.Pending != 0 {
		t.Fatalf("carried title fallback: counts=%#v err=%v", counts, err)
	}
	event, err := eventByID(ctx, service.db, carried.ID)
	if err != nil || event.Description != "carried description" {
		t.Fatalf("carried task after chat update = %#v err=%v", event, err)
	}
	events, err := service.GetBoardEvents(ctx, day)
	if err != nil || len(events) != 1 || events[0].ID != carried.ID {
		t.Fatalf("carried fallback duplicated the card: %#v err=%v", events, err)
	}
}

func TestChatAutoAppliesNewerAIUpdatesAndPreservesOmittedFields(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"
	firstAt := now.Add(-20 * time.Minute)

	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-ai-1"}, []ChatChange{{
		Source:      chatTestEvidence("msg-ai-1", "thread-1", "Planning", firstAt),
		TaskKey:     "ai-task",
		WorkspaceID: 1,
		Title:       "AI task",
		Phase:       PhaseProgressing,
		Description: "first draft",
	}})
	if err != nil || counts.Created != 1 {
		t.Fatalf("create AI task: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("created AI events = %#v err=%v", events, err)
	}
	eventID := events[0].ID
	secondAt := firstAt.Add(10 * time.Minute)

	counts, err = service.ApplyChatPlan(ctx, day, []string{"msg-ai-2"}, []ChatChange{{
		Source:      chatTestEvidence("msg-ai-2", "thread-1", "Planning", secondAt),
		TaskKey:     "ai-task",
		EventID:     eventID,
		WorkspaceID: 1,
		Title:       "AI task",
		Description: "second draft",
		Result:      "interim result",
	}})
	if err != nil || counts.Updated != 1 || counts.Pending != 0 {
		t.Fatalf("newer AI update: counts=%#v err=%v", counts, err)
	}
	event, err := eventByID(ctx, service.db, eventID)
	if err != nil || event.Description != "second draft" || event.Result != "interim result" || event.Phase != PhaseProgressing {
		t.Fatalf("newer AI update fields = %#v err=%v", event, err)
	}

	thirdAt := secondAt.Add(10 * time.Minute)
	counts, err = service.ApplyChatPlan(ctx, day, []string{"msg-ai-3"}, []ChatChange{{
		Source:      chatTestEvidence("msg-ai-3", "thread-1", "Planning", thirdAt),
		TaskKey:     "ai-task",
		EventID:     eventID,
		WorkspaceID: 1,
		Title:       "AI task",
	}})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("omit fields update: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil || event.Description != "second draft" || event.Result != "interim result" || event.Phase != PhaseProgressing {
		t.Fatalf("omitted fields were not preserved: %#v err=%v", event, err)
	}

	counts, err = service.ApplyChatPlan(ctx, day, []string{"msg-ai-old"}, []ChatChange{{
		Source:      chatTestEvidence("msg-ai-old", "thread-1", "Planning", firstAt.Add(5*time.Minute)),
		TaskKey:     "ai-task",
		EventID:     eventID,
		WorkspaceID: 1,
		Title:       "AI task",
		Description: "stale draft",
	}})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("older AI update: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil || event.Description != "second draft" {
		t.Fatalf("older AI update overwrote newer text: %#v err=%v", event, err)
	}
}

func TestChatOwnPrecedingUpdateIsNotAHumanEdit(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	created, err := service.CreateEvent(ctx, EventInput{
		Title: "Run task", Start: day + "T00:00:00", Color: defaultEventColor,
	})
	if err != nil {
		t.Fatalf("create run task: %v", err)
	}
	base := created.UpdatedAt.Format(time.RFC3339Nano)
	now = now.Add(time.Hour)
	firstAt := now.Add(-10 * time.Minute)
	secondAt := now.Add(-5 * time.Minute)

	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-run-1", "msg-run-2"}, []ChatChange{
		{
			Source:      chatTestEvidence("msg-run-1", "thread-1", "Planning", firstAt),
			TaskKey:     "run-one",
			EventID:     created.ID,
			WorkspaceID: 1,
			Title:       "Run task",
			Phase:       PhaseProgressing,
			Description: "draft one",
			BaseUpdated: base,
		},
		{
			Source:      chatTestEvidence("msg-run-2", "thread-2", "Planning", secondAt),
			TaskKey:     "run-two",
			EventID:     created.ID,
			WorkspaceID: 1,
			Title:       "Run task",
			Phase:       PhaseProgressing,
			Description: "draft two",
			Result:      "interim result",
			BaseUpdated: base,
		},
	})
	if err != nil {
		t.Fatalf("apply own-run updates: %v", err)
	}
	if counts.Updated != 2 || counts.Pending != 0 || counts.Skipped != 0 {
		t.Fatalf("own-run counts = %#v", counts)
	}
	event, err := eventByID(ctx, service.db, created.ID)
	if err != nil || event.Description != "draft two" || event.Result != "interim result" || event.Phase != PhaseProgressing {
		t.Fatalf("own-run final fields = %#v err=%v", event, err)
	}
}

func TestChatPendingConflictQueuesCurrentVersionAndCanBeApproved(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	created, err := service.CreateEvent(ctx, EventInput{
		Title: "Manual task", Start: day + "T00:00:00", Color: defaultEventColor,
		Description: "human text", Phase: PhaseProgressing,
	})
	if err != nil {
		t.Fatalf("create manual task: %v", err)
	}
	change := ChatChange{
		Source:      chatTestEvidence("msg-review-conflict", "thread-1", "Planning", now),
		TaskKey:     "manual-task",
		EventID:     created.ID,
		WorkspaceID: 1,
		Title:       "Manual task",
		Phase:       PhaseProgressing,
		Description: "AI text",
		BaseUpdated: created.UpdatedAt.Add(-time.Minute).Format(time.RFC3339Nano),
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-review-conflict"}, []ChatChange{change})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("queue manual conflict: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	if want := created.UpdatedAt.Format(time.RFC3339Nano); pending.Change.BaseUpdated != want {
		t.Fatalf("pending base updated = %q, want current version %q", pending.Change.BaseUpdated, want)
	}
	approved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil {
		t.Fatalf("approve manual conflict: %v", err)
	}
	if approved.State != chatStateApplied {
		t.Fatalf("manual conflict approval state = %q", approved.State)
	}
	event, err := eventByID(ctx, service.db, created.ID)
	if err != nil || event.Description != "AI text" || event.Phase != PhaseProgressing {
		t.Fatalf("approved manual conflict = %#v err=%v", event, err)
	}
}

func TestChatDoneRequiresProposalResultEvenWithExistingResult(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	existing := "existing result"
	created, err := service.CreateEvent(ctx, EventInput{
		Title: "Almost done", Start: day + "T00:00:00", Color: defaultEventColor,
		Phase: PhaseProgressing, Result: &existing,
	})
	if err != nil {
		t.Fatalf("create almost-done task: %v", err)
	}
	change := ChatChange{
		Source:      chatTestEvidence("msg-done-no-result", "thread-1", "Done", now),
		TaskKey:     "almost-done",
		EventID:     created.ID,
		WorkspaceID: 1,
		Title:       "Almost done",
		Phase:       PhaseDone,
		BaseUpdated: created.UpdatedAt.Format(time.RFC3339Nano),
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-done-no-result"}, []ChatChange{change})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("done without proposal result: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	if !strings.Contains(pending.Reason, "result") {
		t.Fatalf("done-without-result reason = %q", pending.Reason)
	}
	resolved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil || resolved.State != chatStateDismissed {
		t.Fatalf("done-without-result approval = %#v err=%v", resolved, err)
	}
	event, err := eventByID(ctx, service.db, created.ID)
	if err != nil || event.Phase != PhaseProgressing || event.Result != existing {
		t.Fatalf("done without proposal result changed the task: %#v err=%v", event, err)
	}
}

func TestChatWorkspaceChangeQueuesWithoutMovingTask(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	created, err := service.CreateEvent(ctx, EventInput{
		Title: "Workspace task", Start: day + "T00:00:00", Color: defaultEventColor,
		WorkspaceID: 2,
	})
	if err != nil {
		t.Fatalf("create workspace task: %v", err)
	}
	change := ChatChange{
		Source:      chatTestEvidence("msg-workspace", "thread-1", "Planning", now),
		TaskKey:     "workspace-task",
		EventID:     created.ID,
		WorkspaceID: 3,
		Title:       "Workspace task",
		Phase:       PhaseTodo,
		BaseUpdated: created.UpdatedAt.Format(time.RFC3339Nano),
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-workspace"}, []ChatChange{change})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("workspace change: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	if !strings.Contains(pending.Reason, "workspace") {
		t.Fatalf("workspace conflict reason = %q", pending.Reason)
	}
	event, err := eventByID(ctx, service.db, created.ID)
	if err != nil || event.WorkspaceID != 2 {
		t.Fatalf("workspace conflict moved the task: %#v err=%v", event, err)
	}
}

func TestChatDoesNotRecreateDeletedTargetOnApproval(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	first := ChatChange{
		Source:  chatTestEvidence("msg-1", "thread-1", "Planning", now),
		TaskKey: "stable-task",
		Title:   "Stable task",
	}
	if counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-1"}, []ChatChange{first}); err != nil || counts.Created != 1 {
		t.Fatalf("create linked task: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("created linked events = %#v err=%v", events, err)
	}
	linkedID := events[0].ID

	review := ChatChange{
		Source:      chatTestEvidence("msg-2", "thread-1", "Planning", now),
		TaskKey:     "stable-task",
		EventID:     linkedID,
		Title:       "Stable task",
		Phase:       PhaseProgressing,
		NeedsReview: true,
	}
	if counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-2"}, []ChatChange{review}); err != nil || counts.Pending != 1 {
		t.Fatalf("queue linked review: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	if err := service.DeleteEvent(ctx, linkedID); err != nil {
		t.Fatalf("delete linked task: %v", err)
	}
	resolved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil {
		t.Fatalf("approve deleted target: %v", err)
	}
	if resolved.State != chatStateDismissed || !strings.Contains(resolved.Reason, "no longer exists") {
		t.Fatalf("deleted target resolution = %#v", resolved)
	}
	after, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(after) != 0 {
		t.Fatalf("approval recreated a deleted task: %#v err=%v", after, err)
	}
}

func TestChatDoesNotCreateAnAlreadyFinishedPriorDayTask(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	change := ChatChange{
		Source:  chatTestEvidence("msg-old-done", "thread-1", "Yesterday", now.AddDate(0, 0, -1)),
		TaskKey: "old-done",
		Title:   "Old finished task",
		Phase:   PhaseDone,
		Result:  "finished yesterday",
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-old-done"}, []ChatChange{change})
	if err != nil {
		t.Fatalf("old done plan: %v", err)
	}
	if counts.Created != 0 || counts.Pending != 0 || counts.Skipped != 1 {
		t.Fatalf("old done counts = %#v", counts)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 0 {
		t.Fatalf("old finished task created: %#v err=%v", events, err)
	}
}

func TestChatProtectsManualClearsPhaseAndTitle(t *testing.T) {
	for _, field := range []string{"description", "phase", "title"} {
		t.Run(field, func(t *testing.T) {
			service := testService(t)
			ctx := context.Background()
			now := boardTestTime(2026, time.March, 2)
			service.now = func() time.Time { return now }
			day := now.Format(dateLayout)
			current, err := service.CreateEvent(ctx, EventInput{Title: "Export captions", Start: day + "T00:00:00", Description: "Keep the original", Phase: PhaseProgressing})
			if err != nil {
				t.Fatal(err)
			}
			change := ChatChange{Source: chatTestEvidence("note", "session", "Export", now.Add(-time.Minute)), TaskKey: "export", EventID: current.ID, WorkspaceID: 1, Title: current.Title, Phase: PhaseProgressing}
			switch field {
			case "description":
				input := eventTestInput(*current)
				input.Description = ""
				_, err = service.UpdateEvent(ctx, current.ID, input)
				change.Description = "Watermarks checked"
			case "phase":
				_, err = service.SetEventPhase(ctx, current.ID, PhaseTodo)
			case "title":
				change.Title = "Renamed captions"
			}
			if err != nil {
				t.Fatal(err)
			}
			count, err := service.ApplyChatPlan(ctx, day, []string{"note"}, []ChatChange{change})
			if err != nil || count.Pending != 1 || count.Updated != 0 {
				t.Fatalf("manual %s was not protected: %+v, %v", field, count, err)
			}
		})
	}
}
