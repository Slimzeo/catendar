package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRegistrationJSONFieldNames(t *testing.T) {
	payload, err := json.Marshal(Event{Registrations: []Registration{{
		Name:                   "考试报名",
		URL:                    "https://example.com/apply",
		Deadline:               "2026-03-10",
		DeadlineText:           "截止 17:00",
		SubmissionDeadline:     "2026-03-20",
		SubmissionDeadlineText: "提交截止 23:59",
	}}})
	if err != nil {
		t.Fatalf("encode event: %v", err)
	}
	for _, want := range []string{
		`"registrations":[`,
		`"name":"考试报名"`,
		`"url":"https://example.com/apply"`,
		`"deadline":"2026-03-10"`,
		`"deadlineText":"截止 17:00"`,
		`"submissionDeadline":"2026-03-20"`,
		`"submissionDeadlineText":"提交截止 23:59"`,
	} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("event JSON %s is missing %s", payload, want)
		}
	}
}

func TestRegistrationOldJSONStaysReadableWithSubmissionFields(t *testing.T) {
	raw := `[{"name":"考试报名","url":"https://example.com/apply","deadline":"2026-03-02","deadlineText":"报名截止 17:00"}]`
	decoded, err := decodeRegistrations(raw)
	if err != nil || len(decoded) != 1 {
		t.Fatalf("old JSON = %#v err=%v", decoded, err)
	}
	if decoded[0].SubmissionDeadline != "" || decoded[0].SubmissionDeadlineText != "" {
		t.Fatalf("old JSON gained a submission deadline: %#v", decoded[0])
	}
	merged, changed := mergeRegistrations(decoded, []Registration{{
		Name:                   "考试报名",
		SubmissionDeadline:     "2026-03-08",
		SubmissionDeadlineText: "提交截止 23:59",
	}}, false)
	if !changed || len(merged) != 1 {
		t.Fatalf("add submission deadline: %#v changed=%v", merged, changed)
	}
	got := merged[0]
	if got.Deadline != "2026-03-02" || got.DeadlineText != "报名截止 17:00" ||
		got.SubmissionDeadline != "2026-03-08" || got.SubmissionDeadlineText != "提交截止 23:59" {
		t.Fatalf("merged old registration = %#v", got)
	}
	encoded, err := encodeRegistrations(merged)
	if err != nil {
		t.Fatalf("encode merged registration: %v", err)
	}
	roundTrip, err := decodeRegistrations(encoded)
	if err != nil || len(roundTrip) != 1 || roundTrip[0] != got {
		t.Fatalf("round trip = %#v err=%v", roundTrip, err)
	}
}

func TestChatRegistrationsFillActionDetailsWithoutFlatteningDescription(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	created, err := service.CreateEvent(ctx, EventInput{
		Title:       "Register for certification",
		Start:       day + "T00:00:00",
		Color:       defaultEventColor,
		Description: "macro summary\n第二行",
		Phase:       PhaseProgressing,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	first := ChatChange{
		Source:  chatTestEvidence("msg-registration-1", "thread-1", "Certification", now),
		TaskKey: "certification-registration",
		EventID: created.ID,
		Title:   created.Title,
		Registrations: []Registration{{
			Name:         "Exam portal",
			URL:          "https://exam.example/apply",
			Deadline:     "2026-03-10",
			DeadlineText: "before 17:00",
		}},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{first.Source.ID}, []ChatChange{first})
	if err != nil || counts.Updated != 1 || counts.Created != 0 || counts.Pending != 0 {
		t.Fatalf("registration-only update: counts=%#v err=%v", counts, err)
	}
	event, err := eventByID(ctx, service.db, created.ID)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	if event.Description != created.Description {
		t.Fatalf("registration update flattened the description: %q", event.Description)
	}
	if len(event.Registrations) != 1 || event.Registrations[0] != first.Registrations[0] {
		t.Fatalf("registrations = %#v", event.Registrations)
	}
	if event.Deadline != "2026-03-10" {
		t.Fatalf("derived deadline = %q, want the registration cutoff", event.Deadline)
	}

	duplicate := first
	duplicate.Source = chatTestEvidence("msg-registration-2", "thread-1", "Certification", now)
	counts, err = service.ApplyChatPlan(ctx, day, []string{duplicate.Source.ID}, []ChatChange{duplicate})
	if err != nil || counts.Skipped != 1 || counts.Updated != 0 {
		t.Fatalf("duplicate registration details: counts=%#v err=%v", counts, err)
	}

	extra := first
	extra.Source = chatTestEvidence("msg-registration-3", "thread-1", "Certification", now)
	extra.Registrations = []Registration{{Name: "Housing", URL: "https://housing.example"}}
	counts, err = service.ApplyChatPlan(ctx, day, []string{extra.Source.ID}, []ChatChange{extra})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("add unrelated registration: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, created.ID)
	if err != nil || len(event.Registrations) != 2 {
		t.Fatalf("unrelated registration was not additive: %#v err=%v", event, err)
	}
	if event.Registrations[0].Name != "Exam portal" || event.Registrations[1].Name != "Housing" {
		t.Fatalf("registration order = %#v", event.Registrations)
	}
	if event.Description != created.Description || event.Deadline != "2026-03-10" {
		t.Fatalf("unrelated update changed existing details: %#v", event)
	}

	summary := ChatChange{
		Source:  chatTestEvidence("msg-registration-4", "thread-1", "Certification", now),
		TaskKey: first.TaskKey,
		EventID: created.ID,
		Title:   created.Title,
		Result:  "submitted",
	}
	counts, err = service.ApplyChatPlan(ctx, day, []string{summary.Source.ID}, []ChatChange{summary})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("summary-only update: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, created.ID)
	if err != nil || len(event.Registrations) != 2 || event.Result != "submitted" {
		t.Fatalf("summary-only update dropped action details: %#v err=%v", event, err)
	}

	manual := eventTestInput(event)
	manual.Description = "edited macro summary"
	manuallyEdited, err := service.UpdateEvent(ctx, created.ID, manual)
	if err != nil || len(manuallyEdited.Registrations) != 2 {
		t.Fatalf("manual edit dropped action details: %#v err=%v", event, err)
	}
	actionOnly := first
	actionOnly.Source = chatTestEvidence("msg-registration-5", "thread-1", "Certification", now)
	actionOnly.Registrations = []Registration{{Name: "Badge", URL: "https://badge.example"}}
	counts, err = service.ApplyChatPlan(ctx, day, []string{actionOnly.Source.ID}, []ChatChange{actionOnly})
	if err != nil || counts.Updated != 1 || counts.Pending != 0 {
		t.Fatalf("action-only update after manual description: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, created.ID)
	if err != nil || len(event.Registrations) != 3 || event.Description != "edited macro summary" {
		t.Fatalf("manual description blocked action details: %#v err=%v", event, err)
	}
	blank := eventTestInput(event)
	blank.Deadline = eventTestStringPointer("")
	manuallyEdited, err = service.UpdateEvent(ctx, created.ID, blank)
	if err != nil || manuallyEdited.Deadline != "" {
		t.Fatalf("manual deadline clear = %q err=%v", event.Deadline, err)
	}

	repeatAfterClear := first
	repeatAfterClear.Source = chatTestEvidence("msg-registration-6", "thread-1", "Certification", now)
	counts, err = service.ApplyChatPlan(ctx, day, []string{repeatAfterClear.Source.ID}, []ChatChange{repeatAfterClear})
	if err != nil || counts.Skipped != 1 {
		t.Fatalf("repeat after manual clear: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, created.ID)
	if err != nil || event.Deadline != "" {
		t.Fatalf("duplicate details refilled a manually cleared deadline: %#v err=%v", event, err)
	}
}

func TestChatRegistrationConflictNeedsReviewAndApprovalOnlyChangesDisputedFields(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	initial := ChatChange{
		Source:  chatTestEvidence("msg-visa-1", "thread-1", "Travel", now),
		TaskKey: "travel-visa",
		Title:   "Travel visa",
		Registrations: []Registration{
			{Name: "Visa", URL: "https://visa.example/a", Deadline: "2026-04-01", DeadlineText: "09:00-17:00"},
			{Name: "Hotel", URL: "https://hotel.example"},
		},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{initial.Source.ID}, []ChatChange{initial})
	if err != nil || counts.Created != 1 {
		t.Fatalf("create with registrations: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("created registrations task = %#v err=%v", events, err)
	}
	eventID := events[0].ID
	if len(events[0].Registrations) != 2 || events[0].Deadline != "2026-04-01" {
		t.Fatalf("created action details = %#v", events[0])
	}

	conflict := ChatChange{
		Source:  chatTestEvidence("msg-visa-2", "thread-1", "Travel", now),
		TaskKey: initial.TaskKey,
		EventID: eventID,
		Title:   initial.Title,
		Registrations: []Registration{{
			Name:         "Visa",
			URL:          "https://visa.example/b",
			Deadline:     "2026-04-01",
			DeadlineText: "09:00-17:00",
		}},
	}
	counts, err = service.ApplyChatPlan(ctx, day, []string{conflict.Source.ID}, []ChatChange{conflict})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("incompatible URL: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	if len(pending.Change.Registrations) != 1 || pending.Change.Registrations[0].URL != "https://visa.example/b" {
		t.Fatalf("pending registration preview = %#v", pending.Change.Registrations)
	}
	if pending.Current == nil || len(pending.Current.Registrations) != 2 {
		t.Fatalf("pending current card = %#v", pending.Current)
	}
	var storedPending string
	if err := service.db.QueryRow("SELECT registrations FROM chat_activities WHERE id = ?", pending.ID).Scan(&storedPending); err != nil ||
		!strings.Contains(storedPending, "visa.example/b") {
		t.Fatalf("pending registration was not persisted: %q err=%v", storedPending, err)
	}
	approved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil || approved.State != chatStateApplied {
		t.Fatalf("approve URL change: %#v err=%v", approved, err)
	}
	event, err := eventByID(ctx, service.db, eventID)
	if err != nil {
		t.Fatalf("read approved task: %v", err)
	}
	if event.Registrations[0].URL != "https://visa.example/b" ||
		event.Registrations[0].Deadline != "2026-04-01" ||
		event.Registrations[0].DeadlineText != "09:00-17:00" {
		t.Fatalf("approved URL changed other registration fields: %#v", event.Registrations[0])
	}
	if len(event.Registrations) != 2 || event.Registrations[1].Name != "Hotel" ||
		event.Registrations[1].URL != "https://hotel.example" {
		t.Fatalf("approval dropped an unrelated registration: %#v", event.Registrations)
	}

	deadlineConflict := conflict
	deadlineConflict.Source = chatTestEvidence("msg-visa-3", "thread-1", "Travel", now)
	deadlineConflict.Registrations = []Registration{{
		Name:         "Visa",
		URL:          "https://visa.example/b",
		Deadline:     "2026-04-02",
		DeadlineText: "12:00-16:00",
	}}
	counts, err = service.ApplyChatPlan(ctx, day, []string{deadlineConflict.Source.ID}, []ChatChange{deadlineConflict})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("incompatible deadline: counts=%#v err=%v", counts, err)
	}
	pending = pendingChatActivity(t, service)
	if !strings.Contains(pending.Reason, "registration") || !strings.Contains(pending.Reason, "deadline") {
		t.Fatalf("registration conflict reason = %q", pending.Reason)
	}
	approved, err = service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil || approved.State != chatStateApplied {
		t.Fatalf("approve deadline change: %#v err=%v", approved, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil {
		t.Fatalf("read deadline-approved task: %v", err)
	}
	visa := event.Registrations[0]
	if visa.URL != "https://visa.example/b" || visa.Deadline != "2026-04-02" || visa.DeadlineText != "12:00-16:00" {
		t.Fatalf("approved deadline fields = %#v", visa)
	}
	if event.Deadline != "2026-04-02" {
		t.Fatalf("AI-derived task deadline did not follow the approved correction: %q", event.Deadline)
	}
	if len(event.Registrations) != 2 || event.Registrations[1].Name != "Hotel" {
		t.Fatalf("deadline approval dropped an unrelated registration: %#v", event.Registrations)
	}
}

func TestRegistrationValidationRejectsUnsafeOrIncompleteDetails(t *testing.T) {
	invalid := []Registration{
		{Name: "", URL: "https://example.com"},
		{Name: "No target"},
		{Name: "FTP", URL: "ftp://example.com/apply"},
		{Name: "Credentials", URL: "https://user:pass@example.com/apply"},
		{Name: "Missing host", URL: "https:///apply"},
		{Name: "Control", URL: "https://example.com/\napply"},
		{Name: "Bad date", URL: "https://example.com", Deadline: "2026-02-30"},
		{Name: "Bad format", URL: "https://example.com", Deadline: "2026-3-2"},
		{Name: strings.Repeat("n", maxRegistrationName+1), URL: "https://example.com"},
		{Name: "Long URL", URL: "https://example.com/" + strings.Repeat("a", maxRegistrationURL)},
		{Name: "Long text", URL: "https://example.com", DeadlineText: strings.Repeat("t", maxRegistrationText+1)},
	}
	for _, registration := range invalid {
		if _, err := normalizeRegistrations([]Registration{registration}); err == nil {
			t.Fatalf("invalid registration %#v was accepted", registration)
		}
	}

	valid := []Registration{{
		Name:         "考试报名",
		URL:          "https://example.com/apply?round=1",
		Deadline:     "2026-03-10",
		DeadlineText: "截止 17:00",
	}}
	normalized, err := normalizeRegistrations(valid)
	if err != nil || len(normalized) != 1 {
		t.Fatalf("valid registration = %#v err=%v", normalized, err)
	}
	if normalized[0].Name != "考试报名" || normalized[0].DeadlineText != "截止 17:00" {
		t.Fatalf("Chinese registration text changed: %#v", normalized[0])
	}

	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"
	change := ChatChange{
		Source:        chatTestEvidence("msg-invalid-registration", "thread-1", "Exam", now),
		TaskKey:       "invalid-registration",
		Title:         "Invalid registration",
		Registrations: []Registration{{Name: "Unsafe", URL: "javascript:alert(1)"}},
	}
	if _, err := service.ApplyChatPlan(ctx, day, []string{change.Source.ID}, []ChatChange{change}); err == nil {
		t.Fatal("plan with an unsafe registration was accepted")
	}
	unseen, err := service.UnseenChatSources(ctx, []string{change.Source.ID})
	if err != nil || !unseen[change.Source.ID] {
		t.Fatalf("rejected plan marked its source seen: %#v err=%v", unseen, err)
	}
	if events, err := service.GetEventsByDateRange(ctx, day, day); err != nil || len(events) != 0 {
		t.Fatalf("rejected plan wrote events: %#v err=%v", events, err)
	}
}

func TestRegistrationValidationKeepsDeadlineKindsIndependent(t *testing.T) {
	valid := []Registration{
		{Name: "提交材料", SubmissionDeadline: "2026-03-08"},
		{Name: "Submission text only", SubmissionDeadlineText: "提交截止 23:59"},
		{Name: "Signup only", Deadline: "2026-03-02"},
	}
	normalized, err := normalizeRegistrations(valid)
	if err != nil || len(normalized) != len(valid) {
		t.Fatalf("valid registrations = %#v err=%v", normalized, err)
	}
	if normalized[0].Deadline != "" || normalized[0].SubmissionDeadline != "2026-03-08" {
		t.Fatalf("submission-only registration = %#v", normalized[0])
	}
	if normalized[1].DeadlineText != "" || normalized[1].SubmissionDeadlineText != "提交截止 23:59" {
		t.Fatalf("submission text-only registration = %#v", normalized[1])
	}
	if normalized[2].Deadline != "2026-03-02" || normalized[2].SubmissionDeadline != "" {
		t.Fatalf("signup-only registration = %#v", normalized[2])
	}

	invalid := []Registration{
		{Name: "Bad submission date", SubmissionDeadline: "2026-02-30"},
		{Name: "Bad submission format", SubmissionDeadline: "2026-3-8"},
		{Name: "Long submission text", SubmissionDeadlineText: strings.Repeat("t", maxRegistrationText+1)},
		{Name: "Unsafe submission link", URL: "javascript:alert(1)", SubmissionDeadline: "2026-03-08"},
	}
	for _, registration := range invalid {
		if _, err := normalizeRegistrations([]Registration{registration}); err == nil {
			t.Fatalf("invalid registration %#v was accepted", registration)
		}
	}
}

func TestRegistrationAliasesDoNotCollapseSharedDirectoryLinks(t *testing.T) {
	for _, link := range []string{"https://forms.example", "https://forms.example/"} {
		merged, changed := mergeRegistrations(
			[]Registration{{Name: "Alpha track", URL: link}},
			[]Registration{{Name: "Alpha track submission", URL: link}},
			false,
		)
		if !changed || len(merged) != 2 {
			t.Fatalf("directory link %q merged distinct activities: %#v", link, merged)
		}
	}
	aliased, changed := mergeRegistrations(
		[]Registration{{Name: "Workshop", URL: "https://forms.example/orbit"}},
		[]Registration{{Name: "Orbit Workshop", URL: "https://forms.example/orbit", Deadline: "2026-03-02"}},
		false,
	)
	if !changed || len(aliased) != 1 || aliased[0].Name != "Workshop" || aliased[0].Deadline != "2026-03-02" {
		t.Fatalf("safe alias was not preserved: %#v changed=%v", aliased, changed)
	}
}

func TestRegistrationDerivedDeadlineUsesBothCutoffKinds(t *testing.T) {
	cases := []struct {
		name          string
		registrations []Registration
		today         string
		want          string
	}{
		{
			name: "nearest future across kinds",
			registrations: []Registration{
				{Name: "Signup", Deadline: "2026-03-20"},
				{Name: "Submission", SubmissionDeadline: "2026-03-10"},
			},
			today: "2026-03-02",
			want:  "2026-03-10",
		},
		{
			name:          "submission only",
			registrations: []Registration{{Name: "Submission", SubmissionDeadline: "2026-03-08"}},
			today:         "2026-03-02",
			want:          "2026-03-08",
		},
		{
			name: "latest past across kinds",
			registrations: []Registration{
				{Name: "Signup", Deadline: "2026-02-01"},
				{Name: "Submission", SubmissionDeadline: "2026-02-15"},
			},
			today: "2026-03-02",
			want:  "2026-02-15",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveRegistrationDeadline(tc.registrations, tc.today); got != tc.want {
				t.Fatalf("derived deadline = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRegistrationDeadlineDerivationPrefersFutureAndKeepsManualDeadline(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	future, err := service.CreateEvent(ctx, EventInput{
		Title: "Future cutoff", Start: day + "T00:00:00", Color: defaultEventColor,
	})
	if err != nil {
		t.Fatalf("create future task: %v", err)
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-future"}, []ChatChange{{
		Source:  chatTestEvidence("msg-future", "thread-1", "Cutoffs", now),
		TaskKey: "future-cutoff",
		EventID: future.ID,
		Title:   future.Title,
		Registrations: []Registration{
			{Name: "Past", URL: "https://past.example", Deadline: "2026-02-01"},
			{Name: "Future", URL: "https://future.example", Deadline: "2026-03-20"},
		},
	}})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("future cutoff: counts=%#v err=%v", counts, err)
	}
	futureEvent, err := eventByID(ctx, service.db, future.ID)
	if err != nil || futureEvent.Deadline != "2026-03-20" {
		t.Fatalf("derived future deadline = %#v err=%v", futureEvent, err)
	}

	sooner := func(id, date string) {
		t.Helper()
		_, err := service.ApplyChatPlan(ctx, day, []string{id}, []ChatChange{{Source: chatTestEvidence(id, "thread-1", "Cutoffs", now), TaskKey: "future-cutoff", EventID: future.ID, Title: future.Title, Registrations: []Registration{{Name: id, Deadline: date}}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	sooner("earlier-cutoff", "2026-03-12")
	changed, err := eventByID(ctx, service.db, future.ID)
	if err != nil || changed.Deadline != "2026-03-12" {
		t.Fatalf("AI-derived deadline did not advance: %v %s", err, changed.Deadline)
	}
	edited := eventTestInput(changed)
	edited.Deadline = eventTestStringPointer("")
	if _, err := service.UpdateEvent(ctx, future.ID, edited); err != nil {
		t.Fatal(err)
	}
	sooner("another-event", "2026-03-10")
	changed, err = eventByID(ctx, service.db, future.ID)
	if err != nil || changed.Deadline != "" {
		t.Fatal("new related event overwrote a manually cleared deadline")
	}
	manualDeadline := "2026-05-01"
	manual, err := service.CreateEvent(ctx, EventInput{
		Title:    "Manual cutoff",
		Start:    day + "T00:00:00",
		Color:    defaultEventColor,
		Deadline: &manualDeadline,
	})
	if err != nil {
		t.Fatalf("create manual task: %v", err)
	}
	counts, err = service.ApplyChatPlan(ctx, day, []string{"msg-manual"}, []ChatChange{{
		Source:        chatTestEvidence("msg-manual", "thread-1", "Cutoffs", now),
		TaskKey:       "manual-cutoff",
		EventID:       manual.ID,
		Title:         manual.Title,
		Registrations: []Registration{{Name: "Registration", URL: "https://register.example", Deadline: "2026-03-10"}},
	}})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("manual cutoff: counts=%#v err=%v", counts, err)
	}
	manualEvent, err := eventByID(ctx, service.db, manual.ID)
	if err != nil || manualEvent.Deadline != manualDeadline {
		t.Fatalf("existing deadline was overwritten: %#v err=%v", manualEvent, err)
	}

	past, err := service.CreateEvent(ctx, EventInput{
		Title: "Past cutoff", Start: day + "T00:00:00", Color: defaultEventColor,
	})
	if err != nil {
		t.Fatalf("create past task: %v", err)
	}
	counts, err = service.ApplyChatPlan(ctx, day, []string{"msg-past"}, []ChatChange{{
		Source:  chatTestEvidence("msg-past", "thread-1", "Cutoffs", now),
		TaskKey: "past-cutoff",
		EventID: past.ID,
		Title:   past.Title,
		Registrations: []Registration{
			{Name: "Older", URL: "https://older.example", Deadline: "2026-02-01"},
			{Name: "Newer", URL: "https://newer.example", Deadline: "2026-02-15"},
		},
	}})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("past cutoff: counts=%#v err=%v", counts, err)
	}
	pastEvent, err := eventByID(ctx, service.db, past.ID)
	if err != nil || pastEvent.Deadline != "2026-02-15" {
		t.Fatalf("derived past deadline = %#v err=%v", pastEvent, err)
	}
}

func TestChatRegistrationUpdatesStayOnTheirBoardRevision(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()

	first := ChatChange{
		Source:  chatTestEvidence("msg-renewal-1", "thread-1", "Passport", now),
		TaskKey: "renew-passport",
		Title:   "Renew passport",
		Registrations: []Registration{
			{Name: "Passport portal", URL: "https://passport.example"},
			{Name: "Photo", URL: "https://photo.example"},
		},
	}
	counts, err := service.ApplyChatPlan(ctx, "2026-03-02", []string{first.Source.ID}, []ChatChange{first})
	if err != nil || counts.Created != 1 {
		t.Fatalf("create renewal task: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, "2026-03-02", "2026-03-02")
	if err != nil || len(events) != 1 {
		t.Fatalf("renewal events = %#v err=%v", events, err)
	}
	eventID := events[0].ID

	now = boardTestTime(2026, time.March, 3)
	service.now = func() time.Time { return now }
	second := ChatChange{
		Source:  chatTestEvidence("msg-renewal-2", "thread-1", "Passport", now),
		TaskKey: first.TaskKey,
		EventID: eventID,
		Title:   first.Title,
		Registrations: []Registration{{
			Name:     "Appointment",
			URL:      "https://appointment.example",
			Deadline: "2026-03-15",
		}},
	}
	counts, err = service.ApplyChatPlan(ctx, "2026-03-03", []string{second.Source.ID}, []ChatChange{second})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("add renewal appointment: counts=%#v err=%v", counts, err)
	}

	yesterday := requireBoardEvent(t, mustBoard(t, service, "2026-03-02"), eventID)
	if len(yesterday.Registrations) != 2 {
		t.Fatalf("yesterday's revision changed: %#v", yesterday.Registrations)
	}
	today := requireBoardEvent(t, mustBoard(t, service, "2026-03-03"), eventID)
	if len(today.Registrations) != 3 || today.Registrations[2].Name != "Appointment" {
		t.Fatalf("today's revision = %#v", today.Registrations)
	}
	canonical, err := eventByID(ctx, service.db, eventID)
	if err != nil || len(canonical.Registrations) != 3 || canonical.Deadline != "2026-03-15" {
		t.Fatalf("canonical action details = %#v err=%v", canonical, err)
	}
}

func TestRegistrationEquivalentEvidenceKeepsPreciseCutoff(t *testing.T) {
	original := []Registration{{Name: "Orbit Workshop", URL: "https://forms.example/orbit", Deadline: "2026-11-22", DeadlineText: "11 月 22 日 17:00（北京时间）"}}
	next := []Registration{{Name: "Workshop", URL: original[0].URL, Deadline: "2026-11-22", DeadlineText: "截止 11月22日 17:00"}}
	if why := registrationConflictReason(original, next); why != "" {
		t.Fatal(why)
	}
	updated, changed := mergeRegistrations(original, next, false)
	if changed || len(updated) != 1 || updated[0] != original[0] {
		t.Fatalf("duplicate wording changed the card: %+v", updated)
	}
	next[0].DeadlineText = "报名截止 11月22日 18:00"
	if why := registrationConflictReason(original, next); why == "" {
		t.Fatal("incompatible time requires review")
	}
}

func TestChatSubmissionDeadlineDoesNotOverwriteSignupDeadline(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	signup := ChatChange{
		Source:  chatTestEvidence("msg-submission-1", "thread-1", "Competition", now),
		TaskKey: "competition-entry",
		Title:   "Competition entry",
		Registrations: []Registration{{
			Name:         "Competition form",
			URL:          "https://competition.example/apply",
			Deadline:     "2026-03-02",
			DeadlineText: "报名截止 17:00",
		}},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{signup.Source.ID}, []ChatChange{signup})
	if err != nil || counts.Created != 1 {
		t.Fatalf("create signup: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("created card = %#v err=%v", events, err)
	}
	eventID := events[0].ID

	submission := signup
	submission.Source = chatTestEvidence("msg-submission-2", "thread-1", "Competition", now)
	submission.Registrations = []Registration{{
		Name:                   "Competition form",
		SubmissionDeadline:     "2026-03-08",
		SubmissionDeadlineText: "提交截止 23:59",
	}}
	counts, err = service.ApplyChatPlan(ctx, day, []string{submission.Source.ID}, []ChatChange{submission})
	if err != nil || counts.Updated != 1 || counts.Pending != 0 {
		t.Fatalf("add submission deadline: counts=%#v err=%v", counts, err)
	}
	event, err := eventByID(ctx, service.db, eventID)
	if err != nil || len(event.Registrations) != 1 {
		t.Fatalf("card after submission: %#v err=%v", event, err)
	}
	registration := event.Registrations[0]
	if registration.Deadline != "2026-03-02" || registration.DeadlineText != "报名截止 17:00" {
		t.Fatalf("submission was flattened into signup: %#v", registration)
	}
	if registration.SubmissionDeadline != "2026-03-08" || registration.SubmissionDeadlineText != "提交截止 23:59" {
		t.Fatalf("submission deadline = %#v", registration)
	}
	if registration.URL != "https://competition.example/apply" {
		t.Fatalf("submission proposal dropped the existing link: %#v", registration)
	}
	if event.Deadline != "2026-03-02" {
		t.Fatalf("derived deadline = %q, want the nearest signup cutoff", event.Deadline)
	}
	var eventJSON string
	if err := service.db.QueryRow("SELECT registrations FROM events WHERE id = ?", eventID).Scan(&eventJSON); err != nil ||
		!strings.Contains(eventJSON, `"deadline":"2026-03-02"`) ||
		!strings.Contains(eventJSON, `"submissionDeadline":"2026-03-08"`) {
		t.Fatalf("event registrations = %q err=%v", eventJSON, err)
	}
	var dayJSON string
	if err := service.db.QueryRow("SELECT registrations FROM event_days WHERE event_id = ? AND day = ?", eventID, day).Scan(&dayJSON); err != nil ||
		!strings.Contains(dayJSON, `"deadline":"2026-03-02"`) ||
		!strings.Contains(dayJSON, `"submissionDeadline":"2026-03-08"`) {
		t.Fatalf("board registrations = %q err=%v", dayJSON, err)
	}

	replay := submission
	replay.Source = chatTestEvidence("msg-submission-3", "thread-1", "Competition", now)
	counts, err = service.ApplyChatPlan(ctx, day, []string{replay.Source.ID}, []ChatChange{replay})
	if err != nil || counts.Skipped != 1 || counts.Pending != 0 || counts.Updated != 0 {
		t.Fatalf("replayed submission: counts=%#v err=%v", counts, err)
	}

	correction := submission
	correction.Source = chatTestEvidence("msg-submission-4", "thread-1", "Competition", now)
	correction.Registrations = []Registration{{
		Name:               "Competition form",
		SubmissionDeadline: "2026-03-09",
	}}
	counts, err = service.ApplyChatPlan(ctx, day, []string{correction.Source.ID}, []ChatChange{correction})
	if err != nil || counts.Pending != 1 || counts.Updated != 0 {
		t.Fatalf("conflicting submission deadline: counts=%#v err=%v", counts, err)
	}
	pending := pendingChatActivity(t, service)
	if len(pending.Change.Registrations) != 1 ||
		pending.Change.Registrations[0].Deadline != "" ||
		pending.Change.Registrations[0].SubmissionDeadline != "2026-03-09" {
		t.Fatalf("pending submission preview = %#v", pending.Change.Registrations)
	}
	var pendingJSON string
	if err := service.db.QueryRow("SELECT registrations FROM chat_activities WHERE id = ?", pending.ID).Scan(&pendingJSON); err != nil ||
		!strings.Contains(pendingJSON, `"submissionDeadline":"2026-03-09"`) ||
		strings.Contains(pendingJSON, `"deadline"`) {
		t.Fatalf("pending registration JSON = %q err=%v", pendingJSON, err)
	}
	approved, err := service.ResolveChatChange(ctx, pending.ID, true)
	if err != nil || approved.State != chatStateApplied {
		t.Fatalf("approve submission correction: %#v err=%v", approved, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil || len(event.Registrations) != 1 {
		t.Fatalf("card after submission approval: %#v err=%v", event, err)
	}
	registration = event.Registrations[0]
	if registration.Deadline != "2026-03-02" || registration.DeadlineText != "报名截止 17:00" {
		t.Fatalf("accepting a submission correction overwrote signup: %#v", registration)
	}
	if registration.SubmissionDeadline != "2026-03-09" || registration.SubmissionDeadlineText != "" {
		t.Fatalf("approved submission correction = %#v", registration)
	}

	settled := correction
	settled.Source = chatTestEvidence("msg-submission-5", "thread-1", "Competition", now)
	counts, err = service.ApplyChatPlan(ctx, day, []string{settled.Source.ID}, []ChatChange{settled})
	if err != nil || counts.Skipped != 1 || counts.Pending != 0 || counts.Updated != 0 {
		t.Fatalf("replayed approved submission: counts=%#v err=%v", counts, err)
	}
}

func TestChatSubmissionOnlyRegistrationCreatesCard(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	change := ChatChange{
		Source:  chatTestEvidence("msg-submission-only", "thread-1", "Poster", now),
		TaskKey: "submit-poster",
		Title:   "Submit poster",
		Registrations: []Registration{{
			Name:                   "Poster submission",
			SubmissionDeadline:     "2026-03-08",
			SubmissionDeadlineText: "上传截止 23:59",
		}},
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{change.Source.ID}, []ChatChange{change})
	if err != nil || counts.Created != 1 || counts.Pending != 0 {
		t.Fatalf("create submission-only card: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 || len(events[0].Registrations) != 1 {
		t.Fatalf("submission-only card = %#v err=%v", events, err)
	}
	event := events[0]
	registration := event.Registrations[0]
	if registration.Deadline != "" || registration.DeadlineText != "" {
		t.Fatalf("submission-only registration gained signup info: %#v", registration)
	}
	if registration.SubmissionDeadline != "2026-03-08" || registration.SubmissionDeadlineText != "上传截止 23:59" {
		t.Fatalf("submission-only registration = %#v", registration)
	}
	if event.Deadline != "2026-03-08" {
		t.Fatalf("derived deadline = %q, want the submission cutoff", event.Deadline)
	}
}

func TestChatSubmissionDeadlineKeepsManualCardDeadline(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"
	manualDeadline := "2026-05-01"

	created, err := service.CreateEvent(ctx, EventInput{
		Title:    "Manual submit cutoff",
		Start:    day + "T00:00:00",
		Color:    defaultEventColor,
		Deadline: &manualDeadline,
	})
	if err != nil {
		t.Fatalf("create manual task: %v", err)
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{"msg-manual-submission"}, []ChatChange{{
		Source:        chatTestEvidence("msg-manual-submission", "thread-1", "Submit", now),
		TaskKey:       "manual-submit-cutoff",
		EventID:       created.ID,
		Title:         created.Title,
		Registrations: []Registration{{Name: "Deliverable", SubmissionDeadline: "2026-03-08"}},
	}})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("add submission deadline: counts=%#v err=%v", counts, err)
	}
	event, err := eventByID(ctx, service.db, created.ID)
	if err != nil || event.Deadline != manualDeadline {
		t.Fatalf("manual card deadline was overwritten: %#v err=%v", event, err)
	}
	if len(event.Registrations) != 1 || event.Registrations[0].Deadline != "" ||
		event.Registrations[0].SubmissionDeadline != "2026-03-08" {
		t.Fatalf("manual task registration = %#v", event.Registrations)
	}
}

func TestChatSubmissionDeadlineKeepsEarlierBoardRevision(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()

	first := ChatChange{
		Source:  chatTestEvidence("msg-track-1", "thread-1", "Track", now),
		TaskKey: "track-submission",
		Title:   "Track submission",
		Registrations: []Registration{{
			Name:         "Track",
			URL:          "https://track.example/apply",
			Deadline:     "2026-03-02",
			DeadlineText: "报名截止 17:00",
		}},
	}
	counts, err := service.ApplyChatPlan(ctx, "2026-03-02", []string{first.Source.ID}, []ChatChange{first})
	if err != nil || counts.Created != 1 {
		t.Fatalf("create track: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, "2026-03-02", "2026-03-02")
	if err != nil || len(events) != 1 {
		t.Fatalf("track events = %#v err=%v", events, err)
	}
	eventID := events[0].ID

	now = boardTestTime(2026, time.March, 3)
	service.now = func() time.Time { return now }
	second := ChatChange{
		Source:  chatTestEvidence("msg-track-2", "thread-1", "Track", now),
		TaskKey: first.TaskKey,
		EventID: eventID,
		Title:   first.Title,
		Registrations: []Registration{{
			Name:                   "Track",
			SubmissionDeadline:     "2026-03-08",
			SubmissionDeadlineText: "上传截止 23:59",
		}},
	}
	counts, err = service.ApplyChatPlan(ctx, "2026-03-03", []string{second.Source.ID}, []ChatChange{second})
	if err != nil || counts.Updated != 1 {
		t.Fatalf("add submission cutoff: counts=%#v err=%v", counts, err)
	}

	yesterday := requireBoardEvent(t, mustBoard(t, service, "2026-03-02"), eventID)
	if yesterday.Deadline != "2026-03-02" || len(yesterday.Registrations) != 1 ||
		yesterday.Registrations[0].SubmissionDeadline != "" {
		t.Fatalf("earlier revision changed: %#v", yesterday)
	}
	today := requireBoardEvent(t, mustBoard(t, service, "2026-03-03"), eventID)
	if today.Deadline != "2026-03-08" || len(today.Registrations) != 1 ||
		today.Registrations[0].Deadline != "2026-03-02" ||
		today.Registrations[0].SubmissionDeadline != "2026-03-08" {
		t.Fatalf("today's revision = %#v", today)
	}
}

func TestChatKeepsMoreThanFortyRegistrationCandidates(t *testing.T) {
	service := testService(t)
	now := boardTestTime(2026, time.March, 2)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-03-02"

	candidates := make([]Registration, 0, 45)
	for i := 0; i < 45; i++ {
		candidate := Registration{
			Name: fmt.Sprintf("Candidate %02d", i+1),
			URL:  fmt.Sprintf("https://forms.example/candidate/%02d", i+1),
		}
		if i%2 == 0 {
			candidate.Deadline = "2026-03-10"
		} else {
			candidate.SubmissionDeadline = "2026-03-11"
		}
		candidates = append(candidates, candidate)
	}
	change := ChatChange{
		Source:        chatTestEvidence("msg-many-1", "thread-1", "Candidates", now),
		TaskKey:       "many-candidates",
		Title:         "Many candidates",
		Registrations: candidates,
	}
	counts, err := service.ApplyChatPlan(ctx, day, []string{change.Source.ID}, []ChatChange{change})
	if err != nil || counts.Created != 1 || counts.Pending != 0 {
		t.Fatalf("create many candidates: counts=%#v err=%v", counts, err)
	}
	events, err := service.GetEventsByDateRange(ctx, day, day)
	if err != nil || len(events) != 1 {
		t.Fatalf("many candidate events = %#v err=%v", events, err)
	}
	eventID := events[0].ID
	event, err := eventByID(ctx, service.db, eventID)
	if err != nil || len(event.Registrations) != len(candidates) {
		t.Fatalf("stored candidates = %d err=%v", len(event.Registrations), err)
	}
	for i, want := range candidates {
		if event.Registrations[i] != want {
			t.Fatalf("candidate %d = %#v, want %#v", i, event.Registrations[i], want)
		}
	}
	if event.Deadline != "2026-03-10" {
		t.Fatalf("derived deadline = %q, want the nearest signup cutoff", event.Deadline)
	}
	if why := registrationConflictReason(event.Registrations, candidates); why != "" {
		t.Fatalf("identical candidates reported a conflict: %s", why)
	}
	if registrationsWouldChange(event.Registrations, candidates) {
		t.Fatal("identical candidates reported a change")
	}

	replay := change
	replay.Source = chatTestEvidence("msg-many-2", "thread-1", "Candidates", now)
	counts, err = service.ApplyChatPlan(ctx, day, []string{replay.Source.ID}, []ChatChange{replay})
	if err != nil || counts.Skipped != 1 || counts.Updated != 0 || counts.Pending != 0 {
		t.Fatalf("replayed candidates: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil || len(event.Registrations) != len(candidates) {
		t.Fatalf("replay changed candidates: %d err=%v", len(event.Registrations), err)
	}

	extra := make([]Registration, 0, 8)
	for i := 0; i < 8; i++ {
		extra = append(extra, Registration{
			Name:               fmt.Sprintf("Extra %02d", i+1),
			URL:                fmt.Sprintf("https://forms.example/extra/%02d", i+1),
			SubmissionDeadline: "2026-03-12",
		})
	}
	addition := change
	addition.Source = chatTestEvidence("msg-many-3", "thread-1", "Candidates", now)
	addition.Registrations = extra
	counts, err = service.ApplyChatPlan(ctx, day, []string{addition.Source.ID}, []ChatChange{addition})
	if err != nil || counts.Updated != 1 || counts.Pending != 0 {
		t.Fatalf("add extra candidates: counts=%#v err=%v", counts, err)
	}
	all := append(append([]Registration(nil), candidates...), extra...)
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil || len(event.Registrations) != len(all) {
		t.Fatalf("stored extra candidates = %d err=%v", len(event.Registrations), err)
	}
	if why := registrationConflictReason(event.Registrations, all); why != "" {
		t.Fatalf("added candidates reported a conflict: %s", why)
	}
	if board := requireBoardEvent(t, mustBoard(t, service, day), eventID); len(board.Registrations) != len(all) {
		t.Fatalf("board candidates = %d, want %d", len(board.Registrations), len(all))
	}

	blank := eventTestInput(event)
	blank.Deadline = eventTestStringPointer("")
	if _, err := service.UpdateEvent(ctx, eventID, blank); err != nil {
		t.Fatalf("clear derived deadline: %v", err)
	}
	replayAll := change
	replayAll.Source = chatTestEvidence("msg-many-4", "thread-1", "Candidates", now)
	replayAll.Registrations = all
	counts, err = service.ApplyChatPlan(ctx, day, []string{replayAll.Source.ID}, []ChatChange{replayAll})
	if err != nil || counts.Skipped != 1 || counts.Pending != 0 || counts.Updated != 0 {
		t.Fatalf("replayed candidates after clear: counts=%#v err=%v", counts, err)
	}
	event, err = eventByID(ctx, service.db, eventID)
	if err != nil || event.Deadline != "" || len(event.Registrations) != len(all) {
		t.Fatalf("replay refilled or dropped candidates: %#v err=%v", event, err)
	}
}
