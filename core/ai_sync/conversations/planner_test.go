package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"catendar-wails/core/calendar"
)

type planFixture struct {
	day      string
	messages []Message
	events   []calendar.Event
}

func decodePromptData(t *testing.T, prompt string, target any) {
	t.Helper()
	_, data, ok := strings.Cut(prompt, promptInputMarker)
	if !ok {
		t.Fatal("prompt is missing its JSON payload marker")
	}
	if err := json.Unmarshal([]byte(data), target); err != nil {
		t.Fatalf("decode prompt data: %v", err)
	}
}

func defaultPlanFixture() planFixture {
	dayStart := time.Date(2026, time.November, 9, 10, 0, 0, 0, time.Local)
	return planFixture{
		day: "2026-11-09",
		messages: []Message{
			{
				ID:          "m1",
				ThreadID:    "t1",
				ThreadTitle: "登录修复",
				Project:     "proj",
				Role:        RoleUser,
				Text:        "我们今天把登录接口修好，然后补充登录测试。",
				At:          dayStart.Unix(),
			},
			{
				ID:          "m2",
				ThreadID:    "t1",
				ThreadTitle: "登录修复",
				Project:     "proj",
				Role:        RoleAssistant,
				Text:        "已完成登录接口修复，测试通过。登录修复已整体验收并交付。",
				At:          dayStart.Unix() + 60,
			},
			{
				ID:          "m3",
				ThreadID:    "t2",
				ThreadTitle: "缓存讨论",
				Project:     "backend",
				Role:        RoleUser,
				Text:        "讨论一下假设性的缓存方案，不做安排。",
				At:          dayStart.AddDate(0, 0, -1).Unix(),
			},
		},
		events: []calendar.Event{
			{
				ID:          42,
				Title:       "登录修复",
				Start:       "2026-11-09T09:00:00",
				Phase:       calendar.PhaseTodo,
				Description: "旧描述",
				SummaryKey:  "login-fix",
				UpdatedAt:   time.Date(2026, time.November, 9, 9, 0, 0, 123456789, time.Local),
			},
		},
	}
}

func TestPlanValidatesEvidenceAndFillsSources(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{
		changeJSON("m1", "把登录接口修好", "login-fix", 42, "登录修复", "progressing", "登录接口修复已开始，继续补充测试", "", false),
		changeJSON("m1", "补充登录测试", "login-tests", 0, "补充登录测试", "todo", "下一步补充登录接口测试", "", false),
		changeJSON("m2", "登录修复已整体验收并交付", "login-done", 0, "登录修复完成", "done", "登录修复已完成", "测试通过", false),
	})

	argsPath, stdinPath, schemaPath := setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan conversations: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("unexpected change count: %d", len(changes))
	}

	existing := changes[0]
	if existing.EventID != 42 || existing.Phase != calendar.PhaseProgressing {
		t.Fatalf("unexpected existing target: %#v", existing)
	}
	if existing.BaseUpdated != fixture.events[0].UpdatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected base update: %q", existing.BaseUpdated)
	}
	if existing.Source.ID != "m1" || existing.Source.ThreadID != "t1" || existing.Source.ThreadTitle != "登录修复" || existing.Source.At != fixture.messages[0].At {
		t.Fatalf("unexpected evidence: %#v", existing.Source)
	}
	if existing.Source.Quote != "把登录接口修好" || existing.TaskKey != "login-fix" {
		t.Fatalf("unexpected quote or task key: %#v", existing)
	}
	if changes[1].EventID != 0 || changes[1].BaseUpdated != "" {
		t.Fatalf("unexpected new task target: %#v", changes[1])
	}
	if changes[2].Phase != calendar.PhaseDone || changes[2].Result != "测试通过" || changes[2].Title != "登录修复完成" {
		t.Fatalf("unexpected done task: %#v", changes[2])
	}
	if changes[0].Title != "登录修复" || changes[1].Description != "下一步补充登录接口测试" {
		t.Fatalf("Chinese text was not preserved: %#v", changes)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	args := strings.Split(string(argsData), "\n")
	joined := strings.Join(args, " ")
	for _, required := range []string{
		"exec", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check",
		"--sandbox read-only", `approval_policy="never"`, "--output-schema", "-o",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("planner args do not contain %q: %s", required, joined)
		}
	}
	if strings.Contains(joined, fixture.messages[0].Text) || strings.Contains(joined, "登录接口") {
		t.Fatalf("planner transcript leaked into argv: %s", joined)
	}

	directory := argumentValue(args, "-C")
	if directory == "" {
		t.Fatal("planner did not use a private working directory")
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planner working directory was not cleaned up: %v", err)
	}

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read captured schema: %v", err)
	}
	schemaText := string(schema)
	for _, required := range []string{
		`"additionalProperties": false`, `"required"`, `"sourceId"`, `"needsReview"`,
		`"enum": ["todo", "progressing", "done"]`, `"completionScope"`, `"completionQuote"`,
		`"enum": ["none", "partial", "uncertain", "whole"]`,
	} {
		if !strings.Contains(schemaText, required) {
			t.Fatalf("output schema does not contain %q", required)
		}
	}
	for _, forbidden := range []string{"workspace", "Workspace"} {
		if strings.Contains(schemaText, forbidden) {
			t.Fatalf("output schema still contains %q", forbidden)
		}
	}

	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	prompt := string(stdin)
	for _, required := range []string{
		"不可信", "todo", "progressing", "done", "needsReview", "逐字摘录",
		"completionScope", "completionQuote", "taskKey",
		fixture.messages[0].Text, "登录修复",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("planner prompt does not contain %q", required)
		}
	}
	for _, forbidden := range []string{"workspace", "Workspace"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("planner prompt still contains %q", forbidden)
		}
	}
}

func TestPlanRejectsMalformedModelOutput(t *testing.T) {
	cases := map[string]string{
		"invalid json":       "not json",
		"unknown root field": `{"changes":[],"extra":true}`,
		"missing changes":    `{}`,
		"missing field":      `{"changes":[{"sourceId":"m1"}]}`,
		"invalid phase": modelJSON(t, []map[string]any{
			changeJSON("m1", "把登录接口修好", "fix-login", 0, "修复登录接口", "blocked", "下一步", "", false),
		}),
		"invalid completion scope": modelJSON(t, []map[string]any{
			withCompletion(changeJSON("m1", "把登录接口修好", "fix-login", 0, "修复登录接口", "progressing", "下一步", "", false), "finished", ""),
		}),
		"trailing data": modelJSON(t, []map[string]any{}) + `{"changes":[]}`,
		"result on todo": modelJSON(t, []map[string]any{
			changeJSON("m1", "把登录接口修好", "fix-login", 0, "修复登录接口", "todo", "下一步", "已有结果", false),
		}),
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := defaultPlanFixture()
			setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

			_, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
			if !errors.Is(err, ErrPlannerOutput) {
				t.Fatalf("expected planner output error, got %v", err)
			}
		})
	}
}

func TestPlanDropsUnverifiableProposals(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{
		changeJSON("missing", "不存在的证据", "ghost", 0, "幽灵任务", "todo", "不存在的下一步", "", false),
		changeJSON("m1", "幻觉引文", "hallucinated", 0, "幻觉任务", "todo", "幻觉下一步", "", false),
		changeJSON("m1", "补充登录测试", "missing-event", 999, "不存在的事件", "todo", "下一步", "", false),
		changeJSON("m1", "补充登录测试", "login-tests", 0, "补充登录测试", "todo", "下一步补充登录接口测试", "", false),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan with some verifiable proposals: %v", err)
	}
	if len(changes) != 1 || changes[0].TaskKey != "login-tests" {
		t.Fatalf("unexpected surviving changes: %#v", changes)
	}

	allBad := modelJSON(t, []map[string]any{
		changeJSON("missing", "不存在的证据", "ghost", 0, "幽灵任务", "todo", "不存在的下一步", "", false),
	})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: allBad})
	_, err = Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if !errors.Is(err, ErrPlannerOutput) {
		t.Fatalf("expected failure when every proposal is unverifiable, got %v", err)
	}
}

func TestPlanDoesNotCreateHistoricalDoneTaskToday(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{
		withCompletion(
			changeJSON("m3", "假设性的缓存方案", "cache-partial", 0, "缓存部分完成", "done", "缓存已部分完成", "已完成", false),
			"partial", "假设性的缓存方案",
		),
		changeJSON("m3", "假设性的缓存方案", "cache-done", 0, "缓存完成", "done", "缓存已完成", "已完成", false),
		changeJSON("m1", "补充登录测试", "login-tests", 0, "补充登录测试", "todo", "下一步补充登录接口测试", "", false),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan historical done: %v", err)
	}
	if len(changes) != 1 || changes[0].TaskKey != "login-tests" {
		t.Fatalf("historical done work was not dropped: %#v", changes)
	}
}

func TestPlanDoneWithoutResultStaysProgressing(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{
		changeJSON("m2", "登录修复已整体验收并交付", "login-done", 0, "登录修复完成", "done", "登录修复已完成", "", false),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan done without a result: %v", err)
	}
	if len(changes) != 1 || changes[0].Phase != calendar.PhaseProgressing || changes[0].Result != "" {
		t.Fatalf("done without a result was not downgraded: %#v", changes)
	}
}

func TestPlanDownshiftsIncompleteDoneWithoutRejectingPlan(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{
		withCompletion(
			changeJSON("m2", "测试通过", "done-partial", 0, "登录修复部分完成", "done", "登录接口已修复，测试待确认", "登录修复已完成", false),
			"partial", "测试通过",
		),
		withCompletion(
			changeJSON("m2", "测试通过", "done-uncertain", 0, "登录修复状态不明", "done", "登录修复情况待确认", "登录修复已完成", false),
			"uncertain", "测试通过",
		),
		withCompletion(
			changeJSON("m2", "测试通过", "done-fabricated", 0, "登录修复完成", "done", "登录修复已完成", "登录修复已完成", false),
			"whole", "全部完成",
		),
		changeJSON("m2", "登录修复已整体验收并交付", "done-whole", 0, "登录修复完成", "done", "登录修复已完成", "测试通过", false),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan with incomplete done proposals: %v", err)
	}
	if len(changes) != 4 {
		t.Fatalf("unexpected change count: %d", len(changes))
	}
	if changes[0].Phase != calendar.PhaseProgressing || changes[0].Result != "" {
		t.Fatalf("partial completion was not downgraded with its interim evidence: %#v", changes[0])
	}
	if changes[1].Phase != calendar.PhaseProgressing || changes[1].Result != "" {
		t.Fatalf("uncertain completion kept a misleading result: %#v", changes[1])
	}
	if changes[2].Phase != calendar.PhaseProgressing || changes[2].Result != "" {
		t.Fatalf("unverified whole completion was not downgraded: %#v", changes[2])
	}
	if changes[3].Phase != calendar.PhaseDone || changes[3].Result != "测试通过" {
		t.Fatalf("verified whole completion was downgraded: %#v", changes[3])
	}
}

func TestPlanDeduplicatesRepeatedProjectProposals(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.events[0].Title = "账本迁移"
	fixture.events[0].SummaryKey = "ledger-current"
	fixture.messages = []Message{
		{ID: "m1", ThreadID: "t1", ThreadTitle: "Orbit video", Project: "orbit-video", Role: RoleUser, Text: "orbit-video 开始制作。", At: dayStart.Unix()},
		{ID: "m2", ThreadID: "t2", ThreadTitle: "Orbit export", Project: "orbit-video", Role: RoleAssistant, Text: "orbit-video 正在做导出。", At: dayStart.Add(30 * time.Minute).Unix()},
		{ID: "m3", ThreadID: "t3", ThreadTitle: "Orbit progress", Project: "orbit-video", Role: RoleAssistant, Text: "orbit-video 导出功能在推进。", At: dayStart.Add(2 * time.Hour).Unix()},
		{ID: "m4", ThreadID: "t4", ThreadTitle: "Ledger migration", Project: "ledger-migration", Role: RoleUser, Text: "账本迁移已经开始。", At: dayStart.Unix()},
		{ID: "m5", ThreadID: "t5", ThreadTitle: "Ledger progress", Project: "ledger-migration", Role: RoleAssistant, Text: "账本迁移还在进行。", At: dayStart.Add(3 * time.Hour).Unix()},
	}
	output := modelJSON(t, []map[string]any{
		changeJSON("m1", "orbit-video 开始制作", "orbit-video", 0, "Orbit Video 制作", "progressing", "制作已开始", "", false),
		changeJSON("m2", "orbit-video 正在做导出", "orbit-video", 0, "Orbit Video 导出", "progressing", "导出在进行", "", false),
		changeJSON("m3", "orbit-video 导出功能在推进", "orbit-video", 0, "Orbit Video 导出推进", "progressing", "导出功能在推进", "", false),
		changeJSON("m4", "账本迁移", "ledger-old", 42, "账本迁移", "progressing", "迁移已开始", "", false),
		changeJSON("m5", "账本迁移还在进行", "ledger-current", 0, "账本迁移进行中", "progressing", "迁移在进行", "", false),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("deduplicate proposals: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("duplicate proposals were not collapsed: %#v", changes)
	}
	if changes[0].Source.ID != "m3" || changes[0].TaskKey != "orbit-video" || changes[0].Title != "Orbit Video 导出推进" {
		t.Fatalf("newest project evidence did not win: %#v", changes[0])
	}
	if changes[1].Source.ID != "m5" || changes[1].EventID != 42 || changes[1].TaskKey != "ledger-current" {
		t.Fatalf("newest existing-card evidence did not win: %#v", changes[1])
	}
}

func TestPlanKeepsUnrelatedProjectsSeparate(t *testing.T) {
	dayStart := time.Date(2026, time.November, 9, 10, 0, 0, 0, time.Local)
	fixture := defaultPlanFixture()
	fixture.messages = []Message{
		{ID: "o1", ThreadID: "to", ThreadTitle: "Orbit", Project: "orbit-video", Role: RoleUser, Text: "orbit-video 开始做视频合成。", At: dayStart.Unix()},
		{ID: "l1", ThreadID: "tl", ThreadTitle: "Ledger", Project: "ledger-migration", Role: RoleUser, Text: "账本迁移项目开始。", At: dayStart.Add(5 * time.Minute).Unix()},
	}
	output := modelJSON(t, []map[string]any{
		changeJSON("o1", "orbit-video 开始做视频合成", "orbit-video", 0, "Orbit Video 视频合成", "progressing", "视频合成工作已开始", "", false),
		changeJSON("l1", "账本迁移项目开始", "ledger-migration", 0, "账本迁移项目", "progressing", "账本迁移已开始", "", false),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan unrelated projects: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("unrelated projects were merged or split: %#v", changes)
	}
	if changes[0].TaskKey != "orbit-video" || !strings.Contains(changes[0].Title, "Orbit Video") {
		t.Fatalf("unexpected orbit project card: %#v", changes[0])
	}
	if changes[1].TaskKey != "ledger-migration" || !strings.Contains(changes[1].Title, "账本迁移") {
		t.Fatalf("unexpected ledger project card: %#v", changes[1])
	}
	for _, change := range changes {
		if change.EventID != 0 {
			t.Fatalf("unexpected existing card target: %#v", change)
		}
	}
}

func TestPlanReportsCLIFailureWithoutLeakingTranscript(t *testing.T) {
	fixture := defaultPlanFixture()
	setPlannerFixtureEnv(t, fakePlannerFixture{
		Stderr:   "SECRET stderr " + fixture.messages[0].Text,
		ExitCode: 1,
	})

	_, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if !errors.Is(err, ErrPlannerUnavailable) {
		t.Fatalf("expected planner unavailable error, got %v", err)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "登录接口") {
		t.Fatalf("CLI error leaked raw output: %v", err)
	}
}

func TestPlanHonorsCancellationAndCleansTempDirectory(t *testing.T) {
	fixture := defaultPlanFixture()
	argsPath, _, _ := setPlannerFixtureEnv(t, fakePlannerFixture{DelayMillis: 10000})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := Plan(ctx, os.Args[0], fixture.day, fixture.events, fixture.messages)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	directory := argumentValue(strings.Split(string(argsData), "\n"), "-C")
	if directory == "" {
		t.Fatal("planner did not use a private working directory")
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled planner left its temp directory behind: %v", err)
	}
}

func TestPlanWithNoMessagesIsEmpty(t *testing.T) {
	fixture := defaultPlanFixture()
	changes, err := Plan(context.Background(), "unused-binary", fixture.day, fixture.events, nil)
	if err != nil {
		t.Fatalf("plan empty messages: %v", err)
	}
	if changes == nil || len(changes) != 0 {
		t.Fatalf("expected a non-nil empty change list, got %#v", changes)
	}
}

func TestNormalizeTaskKeyIsStable(t *testing.T) {
	cases := map[string]string{
		"Fix Login Flow": "fix-login-flow",
		"fix--login":     "fix-login",
		" 修复 登录 ":        "修复-登录",
		"API_v2":         "api-v2",
		"修复，登录":          "修复-登录",
		"修复.登录":          "修复-登录",
		"修复 登录，并补充测试":    "修复-登录-并补充测试",
		"café":           "café",
		"修复！？":           "修复",
		"Orbit Video":    "orbit-video",
	}
	for input, want := range cases {
		if got := normalizeTaskKey(input); got != want {
			t.Fatalf("normalizeTaskKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEvidenceQuoteRequiresExactSubstring(t *testing.T) {
	if _, ok := evidenceQuote("请修复登录问题", "修复登录"); !ok {
		t.Fatal("exact substring was rejected")
	}
	if _, ok := evidenceQuote("请修复登录问题", "修复 登录"); ok {
		t.Fatal("non-exact substring was accepted")
	}
	if _, ok := evidenceQuote("请修复登录问题", " 修复登录 "); !ok {
		t.Fatal("incidental surrounding whitespace should be trimmed")
	}
}

func TestPlanAllowsInterimResultForProgressing(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{
		changeJSON("m1", "把登录接口修好", "login-fix", 42, "登录修复", "progressing", "登录接口修复中", "已修复一半", false),
	})

	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan with an interim result: %v", err)
	}
	if len(changes) != 1 || changes[0].Phase != calendar.PhaseProgressing || changes[0].Result != "已修复一半" {
		t.Fatalf("unexpected interim result change: %#v", changes)
	}
}

func TestPlanResolvesShortPromptReferencesForDigestIDs(t *testing.T) {
	fixture := defaultPlanFixture()
	text := "把登录接口修好并补充测试"
	fullID := messageDigest("t1", "turn-digest", "item-digest", RoleUser, text)
	fixture.messages = []Message{{
		ID:          fullID,
		ThreadID:    "t1",
		ThreadTitle: "登录修复",
		Project:     "proj",
		Role:        RoleUser,
		Text:        text,
		At:          fixture.messages[0].At,
	}}
	output := modelJSON(t, []map[string]any{
		changeJSON("m1", "把登录接口修好", "login-fix", 0, "登录修复", "todo", "下一步补充登录测试", "", false),
	})

	_, stdinPath, _ := setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan with digest ids: %v", err)
	}
	if len(changes) != 1 || changes[0].Source.ID != fullID {
		t.Fatalf("short reference was not translated back: %#v", changes)
	}

	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	prompt := string(stdin)
	if strings.Contains(prompt, fullID) {
		t.Fatal("prompt exposed a full digest id")
	}
	var payload promptPayload
	decodePromptData(t, prompt, &payload)
	if len(payload.Conversations) != 1 || len(payload.Conversations[0].Messages) != 1 || payload.Conversations[0].Messages[0].ID != "m1" {
		t.Fatal("the JSON input did not use the short message ID")
	}
}

func TestPlanTreatsContextOnlyMessagesAsContextOnly(t *testing.T) {
	fixture := defaultPlanFixture()
	fixture.messages = []Message{{
		ID:          "ctx",
		ThreadID:    "t1",
		ThreadTitle: "登录修复",
		Project:     "proj",
		Role:        RoleUser,
		Text:        "昨天讨论过登录修复方案",
		ContextOnly: true,
		At:          fixture.messages[0].At,
	}}

	newTask := modelJSON(t, []map[string]any{
		changeJSON("ctx", "登录修复方案", "login-fix", 0, "修复登录", "todo", "下一步修复登录", "", false),
	})
	_, stdinPath, _ := setPlannerFixtureEnv(t, fakePlannerFixture{Output: newTask})

	if changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages); err != nil || len(changes) != 0 {
		t.Fatalf("context-only message should be ignored: %+v, %v", changes, err)
	}

	existing := modelJSON(t, []map[string]any{
		changeJSON("ctx", "登录修复方案", "login-fix", 42, "登录修复", "progressing", "继续修复登录", "", false),
	})
	_, stdinPath, _ = setPlannerFixtureEnv(t, fakePlannerFixture{Output: existing})
	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("context-only message updated an existing task: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("unexpected context-only update: %#v", changes)
	}

	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	prompt := string(stdin)
	var payload promptPayload
	decodePromptData(t, prompt, &payload)
	foundContextOnly := false
	for _, conversation := range payload.Conversations {
		for _, message := range conversation.Messages {
			if message.ID == "ctx" && message.ContextOnly {
				foundContextOnly = true
			}
		}
	}
	if !foundContextOnly {
		t.Fatal("prompt did not mark the context-only message")
	}
}

func TestPlanPromptIncludesExistingSummaryKeyAndLowercaseBrand(t *testing.T) {
	fixture := defaultPlanFixture()
	fixture.events[0].SummaryKey = "login-fix"

	_, stdinPath, _ := setPlannerFixtureEnv(t, fakePlannerFixture{Output: modelJSON(t, []map[string]any{})})

	if _, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages); err != nil {
		t.Fatalf("plan empty changes: %v", err)
	}
	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	prompt := string(stdin)
	if strings.Contains(prompt, "Catendar") {
		t.Fatal("prompt still uses the capitalized brand")
	}
	if !strings.Contains(prompt, "catendar") {
		t.Fatal("prompt does not use the catendar brand")
	}
	for _, forbidden := range []string{"workspace", "Workspace"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt still contains %q", forbidden)
		}
	}

	var payload promptPayload
	decodePromptData(t, prompt, &payload)
	if len(payload.Events) != 1 || payload.Events[0].TaskKey != "login-fix" {
		t.Fatalf("existing event summary key was not included: %#v", payload.Events)
	}

	var raw map[string]any
	decodePromptData(t, prompt, &raw)
	if _, ok := raw["workspaces"]; ok {
		t.Fatal("prompt payload still contains workspaces")
	}
	events, ok := raw["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("unexpected raw event payload: %#v", raw["events"])
	}
	event, ok := events[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected raw event: %#v", events[0])
	}
	if _, ok := event["workspaceId"]; ok {
		t.Fatal("event payload still contains workspaceId")
	}
}

func TestPlanClassifiesCLIErrorsWithoutRawStderr(t *testing.T) {
	cases := map[string]string{
		"401 Unauthorized: invalid api key sk-secretvalue1234567890": "authentication",
		"429 Too Many Requests: quota exceeded":                      "quota",
		"network connection refused while resolving host":            "network",
		"error: unexpected argument '--bogus' found":                 "unknown flag",
	}
	for stderr, want := range cases {
		t.Run(want, func(t *testing.T) {
			fixture := defaultPlanFixture()
			setPlannerFixtureEnv(t, fakePlannerFixture{Stderr: stderr, ExitCode: 1})

			_, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
			if !errors.Is(err, ErrPlannerUnavailable) {
				t.Fatalf("expected unavailable planner error, got %v", err)
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not mention %q", err, want)
			}
			if strings.Contains(err.Error(), stderr) || strings.Contains(err.Error(), "sk-secretvalue") {
				t.Fatalf("CLI error leaked raw stderr: %v", err)
			}
		})
	}
}

func changeJSON(
	sourceID, quote, taskKey string,
	eventID int64,
	title, phase, description, result string,
	needsReview bool,
) map[string]any {
	completionScope := completionNone
	completionQuote := ""
	if phase == calendar.PhaseDone {
		completionScope = completionWhole
		completionQuote = quote
	}
	return map[string]any{
		"sourceId":        sourceID,
		"quote":           quote,
		"taskKey":         taskKey,
		"eventId":         eventID,
		"title":           title,
		"phase":           phase,
		"description":     description,
		"result":          result,
		"needsReview":     needsReview,
		"completionScope": completionScope,
		"completionQuote": completionQuote,
	}
}

func withCompletion(change map[string]any, scope, quote string) map[string]any {
	change["completionScope"] = scope
	change["completionQuote"] = quote
	return change
}

func modelJSON(t *testing.T, changes []map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"changes": changes})
	if err != nil {
		t.Fatalf("encode model output: %v", err)
	}
	return string(encoded)
}

func planOutputPaths(t *testing.T) (args, stdin, schema string) {
	t.Helper()
	directory := t.TempDir()
	return filepath.Join(directory, "args.txt"), filepath.Join(directory, "stdin.txt"), filepath.Join(directory, "schema.json")
}

func setPlannerFixtureEnv(t *testing.T, fixture fakePlannerFixture) (args, stdin, schema string) {
	t.Helper()
	args, stdin, schema = planOutputPaths(t)
	writePlannerFixture(t, fixture)
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", args)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdin)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schema)
	return args, stdin, schema
}

func writePlannerFixture(t *testing.T, fixture fakePlannerFixture) {
	t.Helper()
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("encode planner fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "planner.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write planner fixture: %v", err)
	}
	t.Setenv("CONVERSATIONS_TEST_PLANNER_FIXTURE", path)
}

func TestVerificationReceiptDoesNotCompleteTheProject(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{changeJSON("m2", "测试通过", "login-fix", 42, "登录修复", "done", "所有工作已完成。", "登录项目已交付。", false)})
	setPlannerFixtureEnv(t, fakePlannerFixture{Output: output})
	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.events, fixture.messages)
	if err != nil || len(changes) != 1 || changes[0].Phase != calendar.PhaseProgressing || changes[0].Result != "" || changes[0].Description != "" {
		t.Fatalf("a test receipt was treated as whole-project completion: %+v %v", changes, err)
	}
}

func TestBoardSentenceOmitsStepLists(t *testing.T) {
	for input, want := range map[string]string{
		"试用版已交付。随后合并了改动。":                            "试用版已交付。",
		"Preview ready. Build 53 passed.":            "Preview ready.",
		"交付仍在验收。\n- 一项修复":                            "交付仍在验收。",
		"- PR 123\n- Run tests":                      "",
		strings.Repeat("x", maxBoardSentenceRunes+1): "",
	} {
		if got := boardSentence(input); got != want {
			t.Errorf("boardSentence returned %q; want %q", got, want)
		}
	}
}
