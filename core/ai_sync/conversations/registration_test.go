package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"catendar-wails/core/calendar"
)

func registrationJSON(name, url, deadline, deadlineText string, messageIDs ...string) map[string]any {
	if messageIDs == nil {
		messageIDs = []string{}
	}
	return map[string]any{
		"name":                   name,
		"url":                    url,
		"deadline":               deadline,
		"deadlineText":           deadlineText,
		"submissionDeadline":     "",
		"submissionDeadlineText": "",
		"messageIds":             messageIDs,
	}
}

func withSubmission(registration map[string]any, deadline, deadlineText string) map[string]any {
	registration["submissionDeadline"] = deadline
	registration["submissionDeadlineText"] = deadlineText
	return registration
}

func changeWithRegistrations(change map[string]any, registrations ...map[string]any) map[string]any {
	if registrations == nil {
		registrations = []map[string]any{}
	}
	change["registrations"] = registrations
	return change
}

func TestPlanExtractsMultipleRegistrationsUnderOneCard(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{
		{
			ID: "m1", ThreadID: "t1", ThreadTitle: "竞赛报名", Project: "contests",
			Role: RoleUser, At: dayStart.Unix(),
			Text: "我在准备 ACM 区域赛报名，报名入口是 https://register.example.org/acm ，截止 2026-11-20 23:59（UTC+8）。",
		},
		{
			ID: "m2", ThreadID: "t1", ThreadTitle: "竞赛报名", Project: "contests",
			Role: RoleUser, At: dayStart.Add(10 * time.Minute).Unix(),
			Text: "蓝桥杯报名：https://example.org/lanqiao/apply，12月1日截止。",
		},
		{
			ID: "m3", ThreadID: "foreign", ThreadTitle: "闲聊", Project: "other",
			Role: RoleUser, At: dayStart.Add(20 * time.Minute).Unix(),
			Text: "外语比赛报名 https://foreign.example.net/reg 截止 2026-12-05。",
		},
	}
	fixture.events[0].Title = "竞赛报名"
	fixture.events[0].SummaryKey = "contest-registration"

	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "我在准备 ACM 区域赛报名", "contest-registration", 42, "竞赛报名", "progressing", "两项比赛在准备报名", "", false),
			registrationJSON("ACM 区域赛", "https://register.example.org/acm", "2026-11-20", "截止 2026-11-20 23:59（UTC+8）", "m1"),
			registrationJSON("蓝桥杯", "https://example.org/lanqiao/apply", "2026-12-01", "12月1日截止", "m2"),
			registrationJSON("外语比赛", "https://foreign.example.net/reg", "2026-12-05", "截止 2026-12-05", "m3"),
		),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan registrations: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("registrations split the card: %#v", changes)
	}
	change := changes[0]
	if change.EventID != 42 || len(change.Registrations) != 2 {
		t.Fatalf("unexpected registrations: %#v", change.Registrations)
	}
	first := change.Registrations[0]
	if first.Name != "ACM 区域赛" || first.URL != "https://register.example.org/acm" ||
		first.Deadline != "2026-11-20" || first.DeadlineText != "截止 2026-11-20 23:59（UTC+8）" {
		t.Fatalf("unexpected first registration: %#v", first)
	}
	second := change.Registrations[1]
	if second.Name != "蓝桥杯" || second.URL != "https://example.org/lanqiao/apply" ||
		second.Deadline != "2026-12-01" || second.DeadlineText != "12月1日截止" {
		t.Fatalf("unexpected second registration: %#v", second)
	}
}

func TestPlanRejectsForeignThreadAndHallucinatedRegistrationEvidence(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{
		{
			ID: "m1", ThreadID: "t1", ThreadTitle: "马拉松", Project: "race",
			Role: RoleUser, At: dayStart.Unix(),
			Text: "我决定报名城市马拉松，报名入口 https://run.example.org/signup。",
		},
		{
			ID: "ctx", ThreadID: "t1", ThreadTitle: "马拉松", Project: "race",
			Role: RoleAssistant, At: dayStart.Add(5 * time.Minute).Unix(), ContextOnly: true,
			Text: "城市马拉松报名截止 2026-12-15 18:00（CST）。",
		},
		{
			ID: "foreign", ThreadID: "t2", ThreadTitle: "别的会话", Project: "other",
			Role: RoleUser, At: dayStart.Add(10 * time.Minute).Unix(),
			Text: "另一场比赛报名 https://other.example.net/apply 截止 2026-12-20。",
		},
	}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "我决定报名城市马拉松", "city-race", 0, "城市马拉松", "todo", "准备报名", "", false),
			registrationJSON("城市马拉松", "https://run.example.org/signup", "2026-12-15", "截止 2026-12-15 18:00（CST）", "m1", "ctx"),
			registrationJSON("城市马拉松", "https://hallucinated.example.org/form", "2026-12-16", "截止 2026-12-16", "m1"),
			registrationJSON("另一场比赛", "https://other.example.net/apply", "2026-12-20", "截止 2026-12-20", "foreign"),
		),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan registration evidence: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 1 {
		t.Fatalf("unverified registrations survived: %#v", changes)
	}
	registration := changes[0].Registrations[0]
	if registration.Name != "城市马拉松" || registration.URL != "https://run.example.org/signup" ||
		registration.Deadline != "2026-12-15" || registration.DeadlineText != "截止 2026-12-15 18:00（CST）" {
		t.Fatalf("context corroboration was not accepted: %#v", registration)
	}
}

func TestPlanMergesSeparatelyEvidencedRegistrationsOnSameCard(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{
		{ID: "m1", ThreadID: "t1", ThreadTitle: "系列赛", Project: "series", Role: RoleUser, At: dayStart.Unix(),
			Text: "第一站报名：https://one.example.org/apply"},
		{ID: "m2", ThreadID: "t1", ThreadTitle: "系列赛", Project: "series", Role: RoleUser, At: dayStart.Add(5 * time.Minute).Unix(),
			Text: "第二站报名：https://two.example.org/apply"},
		{ID: "m3", ThreadID: "t1", ThreadTitle: "系列赛", Project: "series", Role: RoleUser, At: dayStart.Add(10 * time.Minute).Unix(),
			Text: "第三站报名：https://three.example.org/apply"},
	}
	fixture.events[0].Title = "系列赛报名"
	fixture.events[0].SummaryKey = "race-series"

	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "第一站报名", "race-series", 42, "系列赛报名", "progressing", "第一站已准备", "", false),
			registrationJSON("第一站", "https://one.example.org/apply", "", "", "m1"),
		),
		changeWithRegistrations(
			changeJSON("m2", "第二站报名", "race-series", 42, "系列赛报名", "progressing", "第二站已准备", "", false),
			registrationJSON("第二站", "https://two.example.org/apply", "", "", "m2"),
		),
		changeWithRegistrations(
			changeJSON("m3", "第三站报名", "race-series", 42, "系列赛报名", "progressing", "第三站已准备", "", false),
			registrationJSON("第三站", "https://three.example.org/apply", "", "", "m3"),
		),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("merge registrations: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("same-card proposals were not deduplicated: %#v", changes)
	}
	if changes[0].Source.ID != "m3" {
		t.Fatalf("newest card evidence did not win: %#v", changes[0].Source)
	}
	registrations := changes[0].Registrations
	if len(registrations) != 3 {
		t.Fatalf("separately evidenced registrations were lost: %#v", registrations)
	}
	for index, want := range []string{"https://one.example.org/apply", "https://two.example.org/apply", "https://three.example.org/apply"} {
		if registrations[index].URL != want {
			t.Fatalf("unexpected registration order: %#v", registrations)
		}
	}
}

func TestPlanKeepsPartialRegistrationsAndDropsUnsafeLinks(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{
		{ID: "m1", ThreadID: "t1", ThreadTitle: "示范赛", Project: "demo", Role: RoleUser, At: dayStart.Unix(),
			Text: "示范赛报名截止 11月20日 23:59（北京时间），报名入口稍后公布。"},
		{ID: "m2", ThreadID: "t1", ThreadTitle: "示范赛", Project: "demo", Role: RoleUser, At: dayStart.Add(5 * time.Minute).Unix(),
			Text: "说明会报名入口：https://register.example.org/briefing"},
		{ID: "m3", ThreadID: "t1", ThreadTitle: "示范赛", Project: "demo", Role: RoleUser, At: dayStart.Add(10 * time.Minute).Unix(),
			Text: "缴费入口：https://pay.example.com/checkout"},
		{ID: "m4", ThreadID: "t1", ThreadTitle: "示范赛", Project: "demo", Role: RoleUser, At: dayStart.Add(15 * time.Minute).Unix(),
			Text: "源码在 https://github.com/example/project"},
		{ID: "m5", ThreadID: "t1", ThreadTitle: "示范赛", Project: "demo", Role: RoleUser, At: dayStart.Add(20 * time.Minute).Unix(),
			Text: "新闻：https://news.example.com/article"},
		{ID: "m6", ThreadID: "t1", ThreadTitle: "示范赛", Project: "demo", Role: RoleUser, At: dayStart.Add(25 * time.Minute).Unix(),
			Text: "报名入口：javascript:alert(1)"},
	}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "示范赛报名截止", "demo-race", 0, "示范赛", "todo", "准备报名", "", false),
			registrationJSON("示范赛", "", "2026-11-20", "报名截止 11月20日 23:59（北京时间）", "m1"),
			registrationJSON("说明会", "https://register.example.org/briefing", "", "", "m2"),
			registrationJSON("缴费", "https://pay.example.com/checkout", "", "", "m3"),
			registrationJSON("源码", "https://github.com/example/project", "", "", "m4"),
			registrationJSON("新闻", "https://news.example.com/article", "", "", "m5"),
			registrationJSON("脚本", "javascript:alert(1)", "", "", "m6"),
		),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan partial registrations: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 2 {
		t.Fatalf("partial or unsafe registrations were mishandled: %#v", changes)
	}
	deadlineOnly := changes[0].Registrations[0]
	if deadlineOnly.Name != "示范赛" || deadlineOnly.URL != "" ||
		deadlineOnly.Deadline != "2026-11-20" || deadlineOnly.DeadlineText != "报名截止 11月20日 23:59（北京时间）" {
		t.Fatalf("cutoff-only registration was not preserved: %#v", deadlineOnly)
	}
	urlOnly := changes[0].Registrations[1]
	if urlOnly.Name != "说明会" || urlOnly.URL != "https://register.example.org/briefing" ||
		urlOnly.Deadline != "" || urlOnly.DeadlineText != "" {
		t.Fatalf("entry-only registration was not preserved: %#v", urlOnly)
	}
}

func TestPlanDoesNotUseEventDayAsRegistrationDeadline(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{{
		ID: "m1", ThreadID: "t1", ThreadTitle: "路跑", Project: "run", Role: RoleUser, At: dayStart.Unix(),
		Text: "城市路跑活动日期 2026-11-20，报名截止 2026-11-15。报名入口：https://register.example.org/race",
	}}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "报名截止 2026-11-15", "city-run", 0, "城市路跑", "todo", "准备报名", "", false),
			registrationJSON("城市路跑", "https://register.example.org/race", "2026-11-20", "活动日期 2026-11-20", "m1"),
		),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan event day: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 1 {
		t.Fatalf("unexpected registration response: %#v", changes)
	}
	registration := changes[0].Registrations[0]
	if registration.URL != "https://register.example.org/race" {
		t.Fatalf("registration entry was lost: %#v", registration)
	}
	if registration.Deadline == "2026-11-20" || registration.DeadlineText == "活动日期 2026-11-20" {
		t.Fatalf("event day was treated as a registration deadline: %#v", registration)
	}
}

func TestPlanRegistrationDateFormatsStayConservative(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{
		{ID: "m1", ThreadID: "t1", ThreadTitle: "日期", Project: "dates", Role: RoleUser, At: dayStart.Unix(),
			Text: "格式一报名：https://one.example.org/apply，截止 2026/11/25。"},
		{ID: "m2", ThreadID: "t1", ThreadTitle: "日期", Project: "dates", Role: RoleUser, At: dayStart.Add(1 * time.Minute).Unix(),
			Text: "格式二报名：https://two.example.org/apply，截止 2026年12月3日。"},
		{ID: "m3", ThreadID: "t1", ThreadTitle: "日期", Project: "dates", Role: RoleUser, At: dayStart.Add(2 * time.Minute).Unix(),
			Text: "格式三报名：https://three.example.org/apply，截止 12/15。"},
		{ID: "m4", ThreadID: "t1", ThreadTitle: "日期", Project: "dates", Role: RoleUser, At: dayStart.Add(3 * time.Minute).Unix(),
			Text: "格式四报名：https://four.example.org/apply，截止 03/04。"},
		{ID: "m5", ThreadID: "t1", ThreadTitle: "日期", Project: "dates", Role: RoleUser, At: dayStart.Add(4 * time.Minute).Unix(),
			Text: "格式五报名：https://five.example.org/apply，截止 1/5。"},
		{ID: "m6", ThreadID: "t1", ThreadTitle: "日期", Project: "dates", Role: RoleUser, At: dayStart.Add(5 * time.Minute).Unix(),
			Text: "格式六报名：https://six.example.org/apply，截止 25/11/2026。"},
	}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "格式一报名", "date-formats", 0, "日期格式", "todo", "测试日期", "", false),
			registrationJSON("格式一", "https://one.example.org/apply", "2026-11-25", "截止 2026/11/25", "m1"),
			registrationJSON("格式二", "https://two.example.org/apply", "2026-12-03", "截止 2026年12月3日", "m2"),
			registrationJSON("格式三", "https://three.example.org/apply", "2026-12-15", "截止 12/15", "m3"),
			registrationJSON("格式四", "https://four.example.org/apply", "2026-03-04", "截止 03/04", "m4"),
			registrationJSON("格式五", "https://five.example.org/apply", "2026-01-05", "截止 1/5", "m5"),
			registrationJSON("格式六", "https://six.example.org/apply", "2026-11-25", "截止 25/11/2026", "m6"),
		),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan date formats: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 6 {
		t.Fatalf("unexpected date registrations: %#v", changes)
	}
	wantDeadlines := []string{"2026-11-25", "2026-12-03", "2026-12-15", "", "", "2026-11-25"}
	for index, want := range wantDeadlines {
		if got := changes[0].Registrations[index].Deadline; got != want {
			t.Fatalf("registration %d deadline = %q, want %q (%#v)", index, got, want, changes[0].Registrations[index])
		}
	}
	if changes[0].Registrations[3].DeadlineText != "截止 03/04" ||
		changes[0].Registrations[4].DeadlineText != "截止 1/5" {
		t.Fatalf("unresolvable cutoff wording was lost: %#v", changes[0].Registrations)
	}
}

func TestPlanRegistrationSchemaAndExistingPromptRegistrations(t *testing.T) {
	fixture := defaultPlanFixture()
	fixture.events[0].Registrations = []calendar.Registration{{
		Name:         "登录比赛",
		URL:          "https://register.example.org/login",
		Deadline:     "2026-12-01",
		DeadlineText: "截止 12月1日",
	}}

	_, stdinPath, schemaPath := setPlannerFixtureEnv(t, fakePlannerFixture{Output: modelJSON(t, []map[string]any{})})

	if _, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages); err != nil {
		t.Fatalf("plan empty registrations: %v", err)
	}

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read captured schema: %v", err)
	}
	schemaText := string(schema)
	for _, required := range []string{
		`"registrations"`, `"deadlineText"`, `"submissionDeadline"`, `"submissionDeadlineText"`, `"messageIds"`,
	} {
		if !strings.Contains(schemaText, required) {
			t.Fatalf("output schema does not contain %q: %s", required, schemaText)
		}
	}
	if strings.Contains(schemaText, `"maxItems"`) {
		t.Fatalf("output schema still caps registrations: %s", schemaText)
	}
	if !json.Valid(schema) {
		t.Fatal("output schema is not valid JSON")
	}

	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	prompt := string(stdin)
	for _, required := range []string{
		"registrations", "deadlineText", "messageIds", "举办日",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("planner prompt does not contain %q", required)
		}
	}
	var payload promptPayload
	decodePromptData(t, prompt, &payload)
	if len(payload.Events) != 1 || len(payload.Events[0].Registrations) != 1 {
		t.Fatalf("existing registrations were not included: %#v", payload.Events)
	}
	if payload.Events[0].Registrations[0].Name != "登录比赛" ||
		payload.Events[0].Registrations[0].URL != "https://register.example.org/login" {
		t.Fatalf("unexpected existing registration: %#v", payload.Events[0].Registrations[0])
	}
}

func TestRegistrationNeverBorrowsAnotherTableRowsURLOrCutoff(t *testing.T) {
	text := "| Orbit Jam | Starts Nov 19 | Apply: https://forms.example/orbit?a=1&b=2 ; deadline Nov 16 17:00 |\n| Alt Jam | Starts Nov 13 | Apply: https://forms.example/alt ; deadline Nov 11 18:00 |"
	m := Message{ID: "s", ThreadID: "x", Role: RoleAssistant, Text: text, At: time.Date(2026, 11, 9, 9, 0, 0, 0, time.Local).Unix()}
	all := map[string]Message{"s": m}
	items := []modelRegistration{{Name: "Orbit Jam", URL: "https://forms.example/alt", Deadline: "2026-11-11", DeadlineText: "deadline Nov 11 18:00", MessageIDs: []string{"s"}}, {Name: "Orbit Jam", URL: "https://forms.example/orbit?a=1", MessageIDs: []string{"s"}}, {Name: "Invented", URL: "https://forms.example/alt", MessageIDs: []string{"s"}}}
	if got := validateRegistrationProposals(&items, m, all, nil); len(got) != 0 {
		t.Fatalf("accepted mismatched event, truncated URL or fabricated label: %+v", got)
	}
}

func TestPlanSeparatesApplicationAndSubmissionCutoffs(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{{
		ID: "m1", ThreadID: "t1", ThreadTitle: "Orbit Jam", Project: "orbit", Role: RoleUser, At: dayStart.Unix(),
		Text: "### 1. Orbit Jam\n\n报名截止：12 月 1 日 23:59（北京时间）。报名表：https://forms.example/orbit\n\n作品提交：12 月 9 日 18:00（北京时间）。\n\n比赛开始：12 月 10 日 10:00。",
	}}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "### 1. Orbit Jam", "orbit-jam", 0, "Orbit Jam", "todo", "准备报名", "", false),
			withSubmission(
				registrationJSON("Orbit Jam", "https://forms.example/orbit", "2026-12-01", "报名截止：12 月 1 日 23:59（北京时间）", "m1"),
				"2026-12-09", "作品提交：12 月 9 日 18:00（北京时间）",
			),
		),
	})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan mixed cutoffs: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 1 {
		t.Fatalf("mixed cutoffs were lost: %#v", changes)
	}
	registration := changes[0].Registrations[0]
	if registration.URL != "https://forms.example/orbit" ||
		registration.Deadline != "2026-12-01" || registration.DeadlineText != "报名截止：12 月 1 日 23:59（北京时间）" ||
		registration.SubmissionDeadline != "2026-12-09" || registration.SubmissionDeadlineText != "作品提交：12 月 9 日 18:00（北京时间）" {
		t.Fatalf("application and submission cutoffs were not kept separate: %#v", registration)
	}
}

func TestPlanSubmissionCutoffBeforeOrAfterCue(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{
		{ID: "m1", ThreadID: "t1", ThreadTitle: "批次一", Project: "orbit", Role: RoleUser, At: dayStart.Unix(),
			Text: "Orbit Jam：12 月 1 日前报名，12 月 9 日前交作品。"},
		{ID: "m2", ThreadID: "t1", ThreadTitle: "批次二", Project: "orbit", Role: RoleUser, At: dayStart.Add(5 * time.Minute).Unix(),
			Text: "Alt Jam：11 月 19 日 19:00 交作品。"},
	}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "12 月 1 日前报名", "orbit-jam", 0, "Orbit Jam", "todo", "准备报名", "", false),
			withSubmission(
				registrationJSON("Orbit Jam", "", "2026-12-01", "12 月 1 日前报名", "m1"),
				"2026-12-09", "12 月 9 日前交作品",
			),
			withSubmission(
				registrationJSON("Alt Jam", "", "", "", "m2"),
				"2026-11-19", "11 月 19 日 19:00",
			),
		),
	})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan submission phrasing: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 2 {
		t.Fatalf("submission cutoffs were lost: %#v", changes)
	}
	first, second := changes[0].Registrations[0], changes[0].Registrations[1]
	if first.Deadline != "2026-12-01" || first.SubmissionDeadline != "2026-12-09" ||
		first.DeadlineText != "12 月 1 日前报名" || first.SubmissionDeadlineText != "12 月 9 日前交作品" {
		t.Fatalf("date-before-cue submission was not separated: %#v", first)
	}
	if second.SubmissionDeadline != "2026-11-19" || second.SubmissionDeadlineText != "11 月 19 日 19:00" {
		t.Fatalf("date-after-cue submission was not kept: %#v", second)
	}
}

func TestPlanAcceptsExplicitEnglishSubmissionCutoff(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{{
		ID: "m1", ThreadID: "t1", ThreadTitle: "Orbit Jam", Project: "orbit", Role: RoleUser, At: dayStart.Unix(),
		Text: "### Orbit Jam\n\nApplications close Dec 1. Registration: https://forms.example/orbit\n\nFinal project submission: Dec 20 at 17:00 UTC.",
	}}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "### Orbit Jam", "orbit-jam", 0, "Orbit Jam", "todo", "准备报名", "", false),
			withSubmission(
				registrationJSON("Orbit Jam", "https://forms.example/orbit", "2026-12-01", "Applications close Dec 1", "m1"),
				"2026-12-20", "Final project submission: Dec 20 at 17:00 UTC",
			),
		),
	})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan English cutoffs: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 1 {
		t.Fatalf("English cutoffs were lost: %#v", changes)
	}
	registration := changes[0].Registrations[0]
	if registration.Deadline != "2026-12-01" || registration.SubmissionDeadline != "2026-12-20" ||
		registration.SubmissionDeadlineText != "Final project submission: Dec 20 at 17:00 UTC" {
		t.Fatalf("English application/submission cutoffs were not separated: %#v", registration)
	}
}

func TestPlanKeepsFortyTableRegistrations(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	var text strings.Builder
	registrations := make([]map[string]any, 0, 40)
	for index := 1; index <= 40; index++ {
		name := fmt.Sprintf("Event %02d", index)
		link := fmt.Sprintf("https://forms.example/e%02d", index)
		fmt.Fprintf(&text, "| %s | Apply: %s | deadline 2026-12-01 10:00 |\n", name, link)
		registrations = append(registrations, registrationJSON(name, link, "2026-12-01", "deadline 2026-12-01 10:00", "m1"))
	}
	fixture := defaultPlanFixture()
	fixture.messages = []Message{{
		ID: "m1", ThreadID: "t1", ThreadTitle: "报名表", Project: "forms", Role: RoleAssistant, At: dayStart.Unix(), Text: text.String(),
	}}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "Event 01", "event-forms", 0, "报名表", "todo", "整理报名资料", "", false),
			registrations...,
		),
	})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan forty registrations: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 40 {
		t.Fatalf("a long registration list was truncated: %#v", changes)
	}
	first, last := changes[0].Registrations[0], changes[0].Registrations[39]
	if first.Name != "Event 01" || first.URL != "https://forms.example/e01" ||
		last.Name != "Event 40" || last.URL != "https://forms.example/e40" || last.Deadline != "2026-12-01" {
		t.Fatalf("unexpected long-list registrations: %#v", changes[0].Registrations)
	}
}

func TestPlanHeadingSectionsStopBeforeTheNextEvent(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{{
		ID: "m1", ThreadID: "t1", ThreadTitle: "双活动", Project: "events", Role: RoleUser, At: dayStart.Unix(),
		Text: "### 1. Orbit Jam\n\n报名表：https://forms.example/orbit\n\n截止 11月20日 23:59\n\n### 2. Alt Jam\n\n报名表：https://forms.example/alt\n\n截止 11月15日 18:00",
	}}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "### 1. Orbit Jam", "events", 0, "双活动", "todo", "准备报名", "", false),
			registrationJSON("Orbit Jam", "https://forms.example/orbit", "2026-11-20", "截止 11月20日 23:59", "m1"),
			registrationJSON("Alt Jam", "https://forms.example/alt", "2026-11-15", "截止 11月15日 18:00", "m1"),
		),
	})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan heading sections: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 2 {
		t.Fatalf("heading sections were lost: %#v", changes)
	}
	first, second := changes[0].Registrations[0], changes[0].Registrations[1]
	if first.Name != "Orbit Jam" || first.URL != "https://forms.example/orbit" || first.Deadline != "2026-11-20" {
		t.Fatalf("Orbit Jam borrowed another heading: %#v", first)
	}
	if second.Name != "Alt Jam" || second.URL != "https://forms.example/alt" || second.Deadline != "2026-11-15" {
		t.Fatalf("Alt Jam borrowed another heading: %#v", second)
	}
}

func TestPlanKeepsDistinctEventsWithOneSharedForm(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{{
		ID: "m1", ThreadID: "t1", ThreadTitle: "共用表单", Project: "events", Role: RoleAssistant, At: dayStart.Unix(),
		Text: "| Orbit Jam | Apply: https://forms.example/apply | deadline 2026-11-20 17:00 |\n| Alt Jam | Apply: https://forms.example/apply | deadline 2026-11-22 18:00 |",
	}}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "Orbit Jam", "events", 0, "共用表单", "todo", "准备报名", "", false),
			registrationJSON("Orbit Jam", "https://forms.example/apply", "2026-11-20", "deadline 2026-11-20 17:00", "m1"),
			registrationJSON("Alt Jam", "https://forms.example/apply", "2026-11-22", "deadline 2026-11-22 18:00", "m1"),
		),
	})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan shared form: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 2 {
		t.Fatalf("a shared form collapsed distinct events: %#v", changes)
	}
}

func TestPlanDoesNotUseEventDateAsSubmissionDeadline(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{{
		ID: "m1", ThreadID: "t1", ThreadTitle: "作品提交", Project: "orbit", Role: RoleUser, At: dayStart.Unix(),
		Text: "### Orbit Jam\n\n活动日期：11 月 20 日。报名截止：11 月 15 日。\n\n作品提交：11 月 10 日 18:00。",
	}}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "### Orbit Jam", "orbit-jam", 0, "Orbit Jam", "todo", "准备报名", "", false),
			withSubmission(
				registrationJSON("Orbit Jam", "", "2026-11-15", "报名截止：11 月 15 日", "m1"),
				"2026-11-20", "活动日期：11 月 20 日",
			),
			withSubmission(
				registrationJSON("Orbit Jam", "", "", "", "m1"),
				"2026-11-10", "作品提交：11 月 10 日 18:00",
			),
		),
	})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan event date: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 1 {
		t.Fatalf("unexpected registration response: %#v", changes)
	}
	registration := changes[0].Registrations[0]
	if registration.Deadline != "2026-11-15" ||
		registration.SubmissionDeadline != "2026-11-10" || registration.SubmissionDeadlineText != "作品提交：11 月 10 日 18:00" {
		t.Fatalf("event or application date was used as submission: %#v", registration)
	}
}

func TestPlanAcceptsSubmissionFromAnIndependentSameThreadMessage(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{
		{ID: "m1", ThreadID: "t1", ThreadTitle: "Orbit Jam", Project: "orbit", Role: RoleUser, At: dayStart.Unix(),
			Text: "Orbit Jam 准备报名。"},
		{ID: "m2", ThreadID: "t1", ThreadTitle: "Orbit Jam", Project: "orbit", Role: RoleAssistant, At: dayStart.Add(5 * time.Minute).Unix(),
			Text: "报名表：https://forms.example/orbit，作品提交：12 月 9 日 18:00。"},
	}
	output := modelJSON(t, []map[string]any{
		changeWithRegistrations(
			changeJSON("m1", "Orbit Jam", "orbit-jam", 0, "Orbit Jam", "todo", "准备报名", "", false),
			withSubmission(
				registrationJSON("Orbit Jam", "https://forms.example/orbit", "", "", "m1", "m2"),
				"2026-12-09", "作品提交：12 月 9 日 18:00",
			),
		),
	})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan independent sources: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Registrations) != 1 {
		t.Fatalf("independent evidence was lost: %#v", changes)
	}
	registration := changes[0].Registrations[0]
	if registration.URL != "https://forms.example/orbit" || registration.SubmissionDeadline != "2026-12-09" {
		t.Fatalf("URL or submission from the second message was lost: %#v", registration)
	}
}

func TestSurveyKeepsEvidenceWithinItsNumberedOrBulletedActivity(t *testing.T) {
	base := time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local).Unix()
	m := Message{ID: "a", ThreadID: "s", Role: RoleAssistant, At: base, Text: "- Orbit 1 报名入口：https://forms.example/o1\n- Orbit 10 报名入口：https://forms.example/o10，11月12日截止"}
	n := Message{ID: "b", ThreadID: "s", Role: RoleAssistant, At: base + 1, Text: "另一个比赛，Nova 报名表：https://forms.example/nova"}
	all := map[string]Message{"a": m, "b": n}
	items := []modelRegistration{{Name: "Orbit 1", URL: "https://forms.example/o10", DeadlineText: "11月12日截止", MessageIDs: []string{"a"}}, {Name: "Orbit 1", URL: "https://forms.example/nova", MessageIDs: []string{"a", "b"}}}
	if got := validateRegistrationProposals(&items, m, all, nil); len(got) != 0 {
		t.Fatalf("crossed an event or message boundary: %+v", got)
	}
}

func TestSurveyUsesTableHeadersAndEnglishNameBoundaries(t *testing.T) {
	m := Message{ID: "s", ThreadID: "x", Role: RoleAssistant, At: time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local).Unix(), Text: "| Event | Registration deadline | Submission deadline | Event day | Official site |\n| -- | -- | -- | -- | -- |\n| Orbit 1 | Nov 16 | Nov 20 | Nov 25 | https://forms.example/one |\n| Orbit 10 | Nov 12 | Nov 17 | Nov 26 | https://forms.example/ten |"}
	facts := []modelRegistration{{Name: "Orbit 1", URL: "https://forms.example/one", Deadline: "2026-11-16", DeadlineText: "Nov 16", SubmissionDeadline: "2026-11-20", SubmissionDeadlineText: "Nov 20", MessageIDs: []string{"s"}}, {Name: "Orbit 10", URL: "https://forms.example/one", DeadlineText: "Nov 26", SubmissionDeadlineText: "Nov 12", MessageIDs: []string{"s"}}}
	got := validateRegistrationProposals(&facts, m, map[string]Message{"s": m}, nil)
	if len(got) != 1 || got[0].Deadline != "2026-11-16" || got[0].SubmissionDeadline != "2026-11-20" || got[0].URL != facts[0].URL {
		t.Fatalf("mixed table headers: %+v", got)
	}
	if !registrationHasName(registrationNorm("You can apply for Orbit 1 now"), registrationNorm("Orbit 1")) || registrationHasName(registrationNorm("Look at Orbit 10 today"), registrationNorm("Orbit 1")) {
		t.Fatal("English event word boundaries lost")
	}
}

func TestReadAndPlanRetainTheLastRowOfALongSurvey(t *testing.T) {
	const last = "| Last Summit | Registration: https://forms.example/last |"
	table := strings.Repeat("| Workshop | Registration information pending |\n", maxMessageBytes/12) + last
	var messages []Message
	appendPublicMessage(&messages, map[string]struct{}{}, "thread", "turn", threadItem{ID: "item"}, "Events", "", RoleAssistant, table, 1)
	prompt, err := buildPrompt("2026-11-10", nil, messages)
	if err != nil || len(messages) != 1 || !strings.HasSuffix(messages[0].Text, last) || !strings.Contains(prompt, last) {
		t.Fatalf("lost the tail of a public event survey: %v", err)
	}
	if len(plannerMessageText(strings.Repeat("x", maxMessageBytes*2))) != maxMessageBytes {
		t.Fatal("ordinary message previews should stay bounded")
	}
}
