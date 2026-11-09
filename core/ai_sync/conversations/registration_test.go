package conversations

import (
	"context"
	"encoding/json"
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
		"name":         name,
		"url":          url,
		"deadline":     deadline,
		"deadlineText": deadlineText,
		"messageIds":   messageIDs,
	}
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
		`"registrations"`, `"deadlineText"`, `"messageIds"`, `"maxItems": 12`,
	} {
		if !strings.Contains(schemaText, required) {
			t.Fatalf("output schema does not contain %q: %s", required, schemaText)
		}
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
