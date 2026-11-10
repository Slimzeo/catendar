package calendar

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRegistrationNotesUsePlainReadableParagraphs(t *testing.T) {
	chinese := []Registration{
		{
			Name:                   "作品提交",
			URL:                    "https://submit.example/poster",
			SubmissionDeadline:     "2026-03-08",
			SubmissionDeadlineText: "2026年3月8日 23:59（北京时间）",
		},
		{Name: "现场报名", Deadline: "2026-03-02", DeadlineText: "17:00"},
		{Name: "线上报名", DeadlineText: "Rolling admissions"},
	}
	got := FormatRegistrationNotes("海报提交", chinese)
	want := "作品提交\n" +
		"作品截止：2026年3月8日 23:59（北京时间）\n" +
		"https://submit.example/poster\n\n" +
		"现场报名\n" +
		"报名截止：2026-03-02 · 17:00\n\n" +
		"线上报名\n" +
		"报名截止：Rolling admissions"
	if got != want {
		t.Fatalf("Chinese notes:\n%s\nwant:\n%s", got, want)
	}
	for _, unwanted := range []string{"Registration deadline", "Submission deadline", "|", "```"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("Chinese notes contain %q: %s", unwanted, got)
		}
	}

	english := FormatRegistrationNotes("Poster submission", []Registration{{
		Name:         "Submission form",
		URL:          "https://poster.example/submit",
		Deadline:     "2026-03-10",
		DeadlineText: "before 17:00 UTC",
	}})
	want = "Submission form\n" +
		"Registration deadline: 2026-03-10 · before 17:00 UTC\n" +
		"https://poster.example/submit"
	if english != want {
		t.Fatalf("English notes:\n%s\nwant:\n%s", english, want)
	}
	if strings.Contains(english, "报名截止") {
		t.Fatalf("English task used Chinese labels: %s", english)
	}
}

func TestChatActivityDetailsJoinExistingDescriptionAsEditableText(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	created, err := service.CreateEvent(ctx, EventInput{
		Title:       "海报提交",
		Start:       day + "T00:00:00",
		Color:       defaultEventColor,
		Description: "原有说明\n第二行手写备注",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	change := ChatChange{
		Source:  chatTestEvidence("msg-description-1", "thread-1", "海报", now),
		TaskKey: "poster-submission",
		EventID: created.ID,
		Title:   created.Title,
		Registrations: []Registration{
			{
				Name:                   "作品提交",
				URL:                    "https://submit.example/poster",
				SubmissionDeadline:     "2026-03-08",
				SubmissionDeadlineText: "2026年3月8日 23:59（北京时间）",
			},
			{Name: "现场报名", Deadline: "2026-03-02", DeadlineText: "Rolling"},
		},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{change.Source.ID}, []ChatChange{change})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("apply details: counts=%#v err=%v", counts, err)
	}
	event, err := eventByID(ctx, service.db, created.ID)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	want := created.Description + "\n\n" + FormatRegistrationNotes(created.Title, change.Registrations)
	if event.Description != want {
		t.Fatalf("description:\n%s\nwant:\n%s", event.Description, want)
	}
	if !strings.HasPrefix(event.Description, created.Description) {
		t.Fatalf("manual notes were not preserved verbatim: %q", event.Description)
	}
	if _, err := service.UpdateEvent(ctx, created.ID, EventInput{
		Title: created.Title, Start: created.Start, Color: created.Color,
		Description: event.Description,
	}); err != nil {
		t.Fatalf("keep edited description: %v", err)
	}
	board := requireBoardEvent(t, mustBoard(t, service, day), created.ID)
	if board.Description != want {
		t.Fatalf("board description diverged from the canonical edit:\n%s", board.Description)
	}
}

func TestSeedBoardUpgradesCanonicalAndHistoricalDescriptionsOnce(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	ctx := context.Background()
	eventID := insertBoardTestEvent(t, service.db, "迁移任务", "2026-03-02T00:00:00", PhaseProgressing,
		"canonical notes", "", "2026-04-01", "2026-02-01 08:00:00")

	eventDetails := []Registration{{
		Name:         "报名入口",
		URL:          "https://migrate.example/apply",
		Deadline:     "2026-03-10",
		DeadlineText: "17:00",
	}}
	dayDetails := []Registration{{
		Name:                   "旧版提交",
		URL:                    "https://migrate.example/submit",
		SubmissionDeadline:     "2026-03-12",
		SubmissionDeadlineText: "23:59",
	}}
	encodedEvent, err := encodeRegistrations(eventDetails)
	if err != nil {
		t.Fatal(err)
	}
	encodedDay, err := encodeRegistrations(dayDetails)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec("UPDATE events SET registrations = ? WHERE id = ?", encodedEvent, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`
		INSERT INTO event_days (
			event_id, day, through_day, title, start, end, color, all_day,
			description, result, bold, phase, deadline, registrations, created_at, updated_at
		) VALUES (?, ?, NULL, ?, ?, ?, ?, 1, ?, '', 0, ?, ?, ?, ?, ?)
	`, eventID, "2026-03-01", "迁移任务旧版", "2026-03-01T00:00:00", "2026-03-01T00:00:00",
		defaultEventColor, "historical notes", PhaseDone, "2026-02-20", encodedDay,
		"2026-02-01 09:00:00", "2026-02-01 09:00:00"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec("INSERT INTO board_meta (id, seeded_at) VALUES (1, ?)", now); err != nil {
		t.Fatal(err)
	}

	if err := SeedBoard(ctx, service.db, now); err != nil {
		t.Fatalf("upgrade descriptions: %v", err)
	}
	var description, phase, deadline string
	var updatedAt time.Time
	if err := service.db.QueryRow("SELECT description, updated_at, phase, deadline FROM events WHERE id = ?", eventID).
		Scan(&description, &updatedAt, &phase, &deadline); err != nil {
		t.Fatal(err)
	}
	wantEvent := "canonical notes\n\n" + FormatRegistrationBlock("迁移任务", eventDetails[0])
	if description != wantEvent {
		t.Fatalf("canonical description:\n%s\nwant:\n%s", description, wantEvent)
	}
	if got := updatedAt.Format("2006-01-02 15:04:05"); got != "2026-02-01 08:00:00" ||
		phase != PhaseProgressing || deadline != "2026-04-01" {
		t.Fatalf("upgrade changed identity fields: updated=%q phase=%q deadline=%q", got, phase, deadline)
	}

	var dayDescription string
	var dayUpdatedAt time.Time
	if err := service.db.QueryRow(
		"SELECT description, updated_at FROM event_days WHERE event_id = ? AND day = ?", eventID, "2026-03-01",
	).Scan(&dayDescription, &dayUpdatedAt); err != nil {
		t.Fatal(err)
	}
	wantDay := "historical notes\n\n" + FormatRegistrationBlock("迁移任务旧版", dayDetails[0])
	if dayDescription != wantDay {
		t.Fatalf("historical description:\n%s\nwant:\n%s", dayDescription, wantDay)
	}
	if got := dayUpdatedAt.Format("2006-01-02 15:04:05"); got != "2026-02-01 09:00:00" {
		t.Fatalf("historical timestamp changed: %q", got)
	}

	if err := SeedBoard(ctx, service.db, now.AddDate(0, 0, 1)); err != nil {
		t.Fatalf("second board seed: %v", err)
	}
	if err := service.db.QueryRow("SELECT description FROM events WHERE id = ?", eventID).Scan(&description); err != nil {
		t.Fatal(err)
	}
	if description != wantEvent {
		t.Fatalf("second run duplicated details:\n%s", description)
	}
	if _, err := service.db.Exec("UPDATE events SET description = '人工改写' WHERE id = ?", eventID); err != nil {
		t.Fatal(err)
	}
	if err := SeedBoard(ctx, service.db, now.AddDate(0, 0, 2)); err != nil {
		t.Fatalf("third board seed: %v", err)
	}
	if err := service.db.QueryRow("SELECT description FROM events WHERE id = ?", eventID).Scan(&description); err != nil {
		t.Fatal(err)
	}
	if description != "人工改写" {
		t.Fatalf("migration restored deleted details: %q", description)
	}
}

func TestReplayingOldDetailsDoesNotRestoreManualDeletions(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	first := Registration{Name: "报名表", URL: "https://forms.example/apply"}
	second := Registration{Name: "住宿", URL: "https://housing.example"}
	third := Registration{Name: "纪念徽章", URL: "https://badge.example"}
	initial := ChatChange{
		Source:        chatTestEvidence("msg-replay-1", "thread-1", "活动", now),
		TaskKey:       "event-notes",
		Title:         "活动资料",
		Description:   "汇总",
		Registrations: []Registration{first, second},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{initial.Source.ID}, []ChatChange{initial})
	if err != nil || counts.Created != 1 {
		t.Fatalf("create details: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("created event: %#v err=%v", events, err)
	}
	eventID := events[0].ID
	blockFirst := FormatRegistrationBlock(initial.Title, first)
	blockSecond := FormatRegistrationBlock(initial.Title, second)
	blockThird := FormatRegistrationBlock(initial.Title, third)

	event, err := eventByID(ctx, service.db, eventID)
	if err != nil || event.Description != "汇总\n\n"+blockFirst+"\n\n"+blockSecond {
		t.Fatalf("initial description = %#v err=%v", event, err)
	}
	manual := eventTestInput(event)
	manual.Description = "人工整理\n\n" + blockFirst
	if _, err := service.UpdateEvent(ctx, eventID, manual); err != nil {
		t.Fatalf("manual edit: %v", err)
	}

	replay := initial
	replay.Source = chatTestEvidence("msg-replay-2", "thread-1", "活动", now)
	replay.Description = ""
	counts, err = service.ApplyChatPlan(ctx, day, []string{replay.Source.ID}, []ChatChange{replay})
	if err != nil || counts.Skipped != 1 || counts.Updated != 0 {
		t.Fatalf("replay old details: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil || event.Description != manual.Description {
		t.Fatalf("replay restored deleted details: %#v err=%v", event, err)
	}

	addition := initial
	addition.Source = chatTestEvidence("msg-replay-3", "thread-1", "活动", now)
	addition.Description = ""
	addition.Registrations = []Registration{third}
	counts, err = service.ApplyChatPlan(ctx, day, []string{addition.Source.ID}, []ChatChange{addition})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("append new details: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	want := "人工整理\n\n" + blockFirst + "\n\n" + blockThird
	if err != nil || event.Description != want {
		t.Fatalf("new details rewrote unrelated notes:\n%s\nwant:\n%s", event.Description, want)
	}
	if strings.Contains(event.Description, "housing.example") {
		t.Fatalf("deleted activity was restored: %s", event.Description)
	}
}

func TestMacroSummaryKeepsPreviouslyAddedActivityNotes(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"
	registration := Registration{Name: "报名入口", URL: "https://forms.example/apply", Deadline: "2026-03-10"}

	first := ChatChange{
		Source:        chatTestEvidence("msg-summary-1", "thread-1", "活动", now.Add(-time.Hour)),
		TaskKey:       "event-summary",
		Title:         "活动资料",
		Description:   "第一版汇总",
		Registrations: []Registration{registration},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{first.Source.ID}, []ChatChange{first})
	if err != nil || counts.Created != 1 {
		t.Fatalf("create summary: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("created event: %#v err=%v", events, err)
	}
	eventID := events[0].ID
	block := FormatRegistrationBlock(first.Title, registration)

	second := ChatChange{
		Source:      chatTestEvidence("msg-summary-2", "thread-1", "活动", now),
		TaskKey:     first.TaskKey,
		EventID:     eventID,
		Title:       first.Title,
		Description: "第二版汇总",
	}
	counts, err = service.ApplyChatPlan(ctx, day, []string{second.Source.ID}, []ChatChange{second})
	if err != nil || counts.Updated != 1 || counts.Pending != 0 {
		t.Fatalf("summary update: counts=%#v err=%v", counts, err)
	}
	event, err := eventByID(ctx, service.db, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if want := "第二版汇总\n\n" + block; event.Description != want {
		t.Fatalf("summary dropped activity notes:\n%s\nwant:\n%s", event.Description, want)
	}
	if len(event.Registrations) != 1 || event.Registrations[0] != registration {
		t.Fatalf("summary changed evidence: %#v", event.Registrations)
	}
}

func TestAcceptedRegistrationConflictPreservesUnrelatedNotes(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	visa := Registration{Name: "签证", URL: "https://visa.example/a"}
	hotel := Registration{Name: "酒店", URL: "https://hotel.example"}
	initial := ChatChange{
		Source:        chatTestEvidence("msg-url-1", "thread-1", "出行", now),
		TaskKey:       "travel-details",
		Title:         "出行资料",
		Description:   "汇总",
		Registrations: []Registration{visa, hotel},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{initial.Source.ID}, []ChatChange{initial})
	if err != nil || counts.Created != 1 {
		t.Fatalf("create travel details: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("created travel event: %#v err=%v", events, err)
	}
	eventID := events[0].ID
	event, err := eventByID(ctx, service.db, eventID)
	if err != nil {
		t.Fatal(err)
	}
	manual := eventTestInput(event)
	manual.Description = "人工备注\n\n" + FormatRegistrationBlock(initial.Title, visa) +
		"\n\n" + FormatRegistrationBlock(initial.Title, hotel)
	if _, err := service.UpdateEvent(ctx, eventID, manual); err != nil {
		t.Fatalf("manual note: %v", err)
	}

	conflict := ChatChange{
		Source:        chatTestEvidence("msg-url-2", "thread-1", "出行", now),
		TaskKey:       initial.TaskKey,
		EventID:       eventID,
		Title:         initial.Title,
		Registrations: []Registration{{Name: "签证", URL: "https://visa.example/b"}},
	}
	counts, err = service.ApplyChatPlan(ctx, day, []string{conflict.Source.ID}, []ChatChange{conflict})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("conflicting URL: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	if pending.Current == nil {
		t.Fatalf("pending current missing: %#v", pending)
	}
	beforeProjection := pending.Current.Description
	projected := ProjectDescription(*pending.Current, pending.Change, true)
	if !strings.Contains(projected, "https://visa.example/b") ||
		!strings.Contains(projected, "https://hotel.example") ||
		!strings.HasPrefix(projected, "人工备注") {
		t.Fatalf("projected description lost notes:\n%s", projected)
	}
	if pending.Current.Description != beforeProjection {
		t.Fatal("projection changed the stored description")
	}
	approved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil || approved.State != chatStateApplied {
		t.Fatalf("approve URL: %#v err=%v", approved, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil {
		t.Fatal(err)
	}
	want := "人工备注\n\n" + FormatRegistrationBlock(initial.Title, Registration{Name: "签证", URL: "https://visa.example/b"}) +
		"\n\n" + FormatRegistrationBlock(initial.Title, hotel)
	if event.Description != want {
		t.Fatalf("approved description:\n%s\nwant:\n%s", event.Description, want)
	}
	if strings.Contains(event.Description, "visa.example/a") {
		t.Fatalf("approved description kept the discarded URL: %s", event.Description)
	}
}

func TestEnrichmentAfterManualRemovalNeedsReviewAndDoesNotRestoreOthers(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	form := Registration{Name: "报名表", URL: "https://forms.example/apply"}
	housing := Registration{Name: "住宿", URL: "https://housing.example"}
	initial := ChatChange{
		Source:        chatTestEvidence("msg-removed-1", "thread-1", "活动", now),
		TaskKey:       "removed-notes",
		Title:         "活动资料",
		Description:   "汇总",
		Registrations: []Registration{form, housing},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{initial.Source.ID}, []ChatChange{initial})
	if err != nil || counts.Created != 1 {
		t.Fatalf("create activity: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("created activity: %#v err=%v", events, err)
	}
	eventID := events[0].ID
	event, err := eventByID(ctx, service.db, eventID)
	if err != nil {
		t.Fatal(err)
	}
	manual := eventTestInput(event)
	manual.Description = "只剩人工备注"
	if _, err := service.UpdateEvent(ctx, eventID, manual); err != nil {
		t.Fatalf("remove details: %v", err)
	}

	enrichment := ChatChange{
		Source:        chatTestEvidence("msg-removed-2", "thread-1", "活动", now),
		TaskKey:       initial.TaskKey,
		EventID:       eventID,
		Title:         initial.Title,
		Registrations: []Registration{{Name: "报名表", URL: "https://forms.example/apply", Deadline: "2026-03-10"}},
	}
	counts, err = service.ApplyChatPlan(ctx, day, []string{enrichment.Source.ID}, []ChatChange{enrichment})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("enrich removed details: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	if !strings.Contains(pending.Reason, "description") || !strings.Contains(pending.Reason, "removed") {
		t.Fatalf("removal conflict reason = %q", pending.Reason)
	}
	approved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil || approved.State != chatStateApplied {
		t.Fatalf("approve enrichment: %#v err=%v", approved, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil {
		t.Fatal(err)
	}
	want := "只剩人工备注\n\n" + FormatRegistrationBlock(initial.Title,
		Registration{Name: "报名表", Deadline: "2026-03-10"})
	if event.Description != want {
		t.Fatalf("accepted enrichment:\n%s\nwant:\n%s", event.Description, want)
	}
	if strings.Contains(event.Description, "housing.example") {
		t.Fatalf("accepted enrichment restored an unrelated deletion: %s", event.Description)
	}
}

func TestReviewedNotesOnlyApplyNewFactsToAnEditedDescription(t *testing.T) {
	portal := Registration{Name: "报名表", URL: "https://forms.example/apply", Deadline: "2026-03-01"}
	poster := Registration{Name: "作品展", URL: "https://gallery.example"}
	event := Event{
		Title: "活动", Registrations: []Registration{portal, poster},
		Description: "第一版汇总\n手写提醒\n\n" + FormatRegistrationBlock("活动", poster) +
			"\n\n请同伴过目\n\n报名表\n报名截止：2026-03-01\n联系主办人",
	}
	change := ChatChange{Title: event.Title, Description: "最新进展", Registrations: []Registration{{
		Name: portal.Name, URL: portal.URL, SubmissionDeadline: "2026-03-12",
	}}}
	activity := ChatActivity{Current: &event, Change: change}
	preview := activity.DescriptionPreview()
	want := strings.Replace(event.Description, "第一版汇总", change.Description, 1) + "\n作品截止：2026-03-12"
	if preview.Change.Description != want || activity.Change.Description != "最新进展" {
		t.Fatalf("incorrect preview or mutated proposal: %q", preview.Change.Description)
	}
	raw, err := json.Marshal(preview)
	if err != nil || strings.Contains(string(raw), `"registrations"`) || strings.Contains(string(raw), portal.URL) {
		t.Fatalf("public payload exposed removed evidence: %s (%v)", raw, err)
	}
}

func TestDescriptionPreviewPrecedesAnActivityWhenThereIsNoSummary(t *testing.T) {
	item := Registration{Name: "Call for posters", URL: "https://art.example/apply"}
	event := Event{Title: "Poster shortlist", Description: FormatRegistrationBlock("Poster shortlist", item), Registrations: []Registration{item}}
	change := ChatChange{Title: event.Title, Description: "Check eligibility."}
	if got := ProjectDescription(event, change, true); got != change.Description+"\n\n"+event.Description {
		t.Fatalf("summary displaced the first activity: %s", got)
	}
}
