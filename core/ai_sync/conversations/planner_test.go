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
	day        string
	messages   []Message
	workspaces []calendar.Workspace
	events     []calendar.Event
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
				Text:        "已完成登录接口修复，测试通过。",
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
		workspaces: []calendar.Workspace{
			{ID: 1, Name: "Routine", Hint: "日常事务"},
			{ID: 2, Name: "Proj", Hint: "项目工作"},
		},
		events: []calendar.Event{
			{
				ID:          42,
				Title:       "登录修复",
				Start:       "2026-11-09T09:00:00",
				Phase:       calendar.PhaseTodo,
				Description: "旧描述",
				UpdatedAt:   time.Date(2026, time.November, 9, 9, 0, 0, 123456789, time.Local),
			},
		},
	}
}

func TestPlanValidatesEvidenceAndFillsSources(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{
		changeJSON("m1", "把登录接口修好", "fix-login", 42, 2, "修复登录接口", "progressing", "登录接口修复已开始，继续补充测试", "", false),
		changeJSON("m1", "补充登录测试", "add-login-tests", 0, 2, "补充登录测试", "todo", "下一步补充登录接口测试", "", false),
		changeJSON("m2", "测试通过", "login-verified", 0, 1, "登录修复完成", "done", "登录修复已完成", "测试通过", false),
	})

	argsPath, stdinPath, schemaPath := planOutputPaths(t)
	writePlannerFixture(t, fakePlannerFixture{Output: output})
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan conversations: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("unexpected change count: %d", len(changes))
	}

	existing := changes[0]
	if existing.EventID != 42 || existing.WorkspaceID != 2 || existing.Phase != calendar.PhaseProgressing {
		t.Fatalf("unexpected existing target: %#v", existing)
	}
	if existing.BaseUpdated != fixture.events[0].UpdatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected base update: %q", existing.BaseUpdated)
	}
	if existing.Source.ID != "m1" || existing.Source.ThreadID != "t1" || existing.Source.ThreadTitle != "登录修复" || existing.Source.At != fixture.messages[0].At {
		t.Fatalf("unexpected evidence: %#v", existing.Source)
	}
	if existing.Source.Quote != "把登录接口修好" || existing.TaskKey != "fix-login" {
		t.Fatalf("unexpected quote or task key: %#v", existing)
	}
	if changes[1].EventID != 0 || changes[1].BaseUpdated != "" {
		t.Fatalf("unexpected new task target: %#v", changes[1])
	}
	if changes[2].Phase != calendar.PhaseDone || changes[2].Result != "测试通过" || changes[2].Title != "登录修复完成" {
		t.Fatalf("unexpected done task: %#v", changes[2])
	}
	if changes[0].Title != "修复登录接口" || changes[1].Description != "下一步补充登录接口测试" {
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
	for _, required := range []string{`"additionalProperties": false`, `"required"`, `"sourceId"`, `"needsReview"`, `"enum": ["todo", "progressing", "done"]`} {
		if !strings.Contains(schemaText, required) {
			t.Fatalf("output schema does not contain %q", required)
		}
	}

	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	prompt := string(stdin)
	for _, required := range []string{"不可信", "todo", "progressing", "done", "needsReview", "exact substring", fixture.messages[0].Text, "登录修复"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("planner prompt does not contain %q", required)
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
			changeJSON("m1", "把登录接口修好", "fix-login", 0, 2, "修复登录接口", "blocked", "下一步", "", false),
		}),
		"trailing data": modelJSON(t, []map[string]any{}) + `{"changes":[]}`,
		"duplicate task": modelJSON(t, []map[string]any{
			changeJSON("m1", "把登录接口修好", "fix-login", 0, 2, "修复登录接口", "todo", "下一步", "", false),
			changeJSON("m1", "补充登录测试", "fix-login", 0, 2, "补充登录测试", "todo", "下一步", "", false),
		}),
		"result on todo": modelJSON(t, []map[string]any{
			changeJSON("m1", "把登录接口修好", "fix-login", 0, 2, "修复登录接口", "todo", "下一步", "已有结果", false),
		}),
		"done without result": modelJSON(t, []map[string]any{
			changeJSON("m1", "把登录接口修好", "fix-login", 0, 2, "修复登录接口", "done", "登录修复已完成", "", false),
		}),
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := defaultPlanFixture()
			argsPath, stdinPath, schemaPath := planOutputPaths(t)
			writePlannerFixture(t, fakePlannerFixture{Output: output})
			t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
			t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
			t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
			t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

			_, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
			if !errors.Is(err, ErrPlannerOutput) {
				t.Fatalf("expected planner output error, got %v", err)
			}
		})
	}
}

func TestPlanDropsUnverifiableProposals(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{
		changeJSON("missing", "不存在的证据", "ghost", 0, 2, "幽灵任务", "todo", "不存在的下一步", "", false),
		changeJSON("m1", "幻觉引文", "hallucinated", 0, 2, "幻觉任务", "todo", "幻觉下一步", "", false),
		changeJSON("m1", "补充登录测试", "fix-login", 999, 2, "不存在的事件", "todo", "下一步", "", false),
		changeJSON("m1", "把登录接口修好", "bad-workspace", 0, 99, "错误工作区", "todo", "下一步", "", false),
		changeJSON("m1", "补充登录测试", "add-login-tests", 0, 2, "补充登录测试", "todo", "下一步补充登录接口测试", "", false),
	})

	argsPath, stdinPath, schemaPath := planOutputPaths(t)
	writePlannerFixture(t, fakePlannerFixture{Output: output})
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan with some verifiable proposals: %v", err)
	}
	if len(changes) != 1 || changes[0].TaskKey != "add-login-tests" {
		t.Fatalf("unexpected surviving changes: %#v", changes)
	}

	allBad := modelJSON(t, []map[string]any{
		changeJSON("missing", "不存在的证据", "ghost", 0, 2, "幽灵任务", "todo", "不存在的下一步", "", false),
	})
	writePlannerFixture(t, fakePlannerFixture{Output: allBad})
	_, err = Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
	if !errors.Is(err, ErrPlannerOutput) {
		t.Fatalf("expected failure when every proposal is unverifiable, got %v", err)
	}
}

func TestPlanDoesNotCreateHistoricalDoneTaskToday(t *testing.T) {
	fixture := defaultPlanFixture()
	output := modelJSON(t, []map[string]any{
		changeJSON("m3", "假设性的缓存方案", "cache-done", 0, 2, "缓存完成", "done", "缓存已完成", "已完成", false),
		changeJSON("m1", "补充登录测试", "add-login-tests", 0, 2, "补充登录测试", "todo", "下一步补充登录接口测试", "", false),
	})

	argsPath, stdinPath, schemaPath := planOutputPaths(t)
	writePlannerFixture(t, fakePlannerFixture{Output: output})
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("plan historical done: %v", err)
	}
	if len(changes) != 1 || changes[0].TaskKey != "add-login-tests" {
		t.Fatalf("historical done task was not dropped: %#v", changes)
	}
}

func TestPlanReportsCLIFailureWithoutLeakingTranscript(t *testing.T) {
	fixture := defaultPlanFixture()
	argsPath, stdinPath, schemaPath := planOutputPaths(t)
	writePlannerFixture(t, fakePlannerFixture{
		Stderr:   "SECRET stderr " + fixture.messages[0].Text,
		ExitCode: 1,
	})
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

	_, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
	if !errors.Is(err, ErrPlannerUnavailable) {
		t.Fatalf("expected planner unavailable error, got %v", err)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "登录接口") {
		t.Fatalf("CLI error leaked raw output: %v", err)
	}
}

func TestPlanHonorsCancellationAndCleansTempDirectory(t *testing.T) {
	fixture := defaultPlanFixture()
	argsPath, stdinPath, schemaPath := planOutputPaths(t)
	writePlannerFixture(t, fakePlannerFixture{DelayMillis: 10000})
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := Plan(ctx, os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
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
	changes, err := Plan(context.Background(), "unused-binary", fixture.day, fixture.workspaces, fixture.events, nil)
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
		changeJSON("m1", "把登录接口修好", "fix-login", 42, 2, "修复登录接口", "progressing", "登录接口修复中", "已修复一半", false),
	})

	argsPath, stdinPath, schemaPath := planOutputPaths(t)
	writePlannerFixture(t, fakePlannerFixture{Output: output})
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
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
		changeJSON("m1", "把登录接口修好", "fix-login", 0, 2, "修复登录接口", "todo", "下一步补充登录测试", "", false),
	})

	argsPath, stdinPath, schemaPath := planOutputPaths(t)
	writePlannerFixture(t, fakePlannerFixture{Output: output})
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
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
	if !strings.Contains(prompt, `"id": "m1"`) {
		t.Fatalf("prompt does not contain the short reference: %s", prompt)
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

	argsPath, stdinPath, schemaPath := planOutputPaths(t)
	newTask := modelJSON(t, []map[string]any{
		changeJSON("ctx", "登录修复方案", "fix-login", 0, 2, "修复登录", "todo", "下一步修复登录", "", false),
	})
	writePlannerFixture(t, fakePlannerFixture{Output: newTask})
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

	if _, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages); !errors.Is(err, ErrPlannerOutput) {
		t.Fatalf("context-only message created a new task: %v", err)
	}

	existing := modelJSON(t, []map[string]any{
		changeJSON("ctx", "登录修复方案", "fix-login", 42, 2, "修复登录", "progressing", "继续修复登录", "", false),
	})
	writePlannerFixture(t, fakePlannerFixture{Output: existing})
	changes, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
	if err != nil {
		t.Fatalf("context-only message updated an existing task: %v", err)
	}
	if len(changes) != 1 || changes[0].EventID != 42 {
		t.Fatalf("unexpected context-only update: %#v", changes)
	}

	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	prompt := string(stdin)
	marker := "Conversation planner input (untrusted JSON):\n"
	index := strings.Index(prompt, marker)
	if index < 0 {
		t.Fatal("prompt is missing the JSON payload marker")
	}
	var payload promptPayload
	if err := json.NewDecoder(strings.NewReader(prompt[index+len(marker):])).Decode(&payload); err != nil {
		t.Fatalf("decode prompt payload: %v", err)
	}
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

func TestPlanPromptIncludesExistingWorkspaceAndLowercaseBrand(t *testing.T) {
	fixture := defaultPlanFixture()
	fixture.events[0].WorkspaceID = 2

	argsPath, stdinPath, schemaPath := planOutputPaths(t)
	writePlannerFixture(t, fakePlannerFixture{Output: modelJSON(t, []map[string]any{})})
	t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
	t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
	t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
	t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

	if _, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages); err != nil {
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

	marker := "Conversation planner input (untrusted JSON):\n"
	index := strings.Index(prompt, marker)
	if index < 0 {
		t.Fatal("prompt is missing the JSON payload marker")
	}
	var payload promptPayload
	if err := json.NewDecoder(strings.NewReader(prompt[index+len(marker):])).Decode(&payload); err != nil {
		t.Fatalf("decode prompt payload: %v", err)
	}
	if len(payload.Events) != 1 || payload.Events[0].WorkspaceID != 2 {
		t.Fatalf("existing event workspace was not included: %#v", payload.Events)
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
			argsPath, stdinPath, schemaPath := planOutputPaths(t)
			writePlannerFixture(t, fakePlannerFixture{Stderr: stderr, ExitCode: 1})
			t.Setenv("CONVERSATIONS_TEST_HELPER", "planner")
			t.Setenv("CONVERSATIONS_TEST_ARGS_FILE", argsPath)
			t.Setenv("CONVERSATIONS_TEST_STDIN_FILE", stdinPath)
			t.Setenv("CONVERSATIONS_TEST_SCHEMA_FILE", schemaPath)

			_, err := Plan(context.Background(), os.Args[0], fixture.day, fixture.workspaces, fixture.events, fixture.messages)
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
	eventID, workspaceID int64,
	title, phase, description, result string,
	needsReview bool,
) map[string]any {
	return map[string]any{
		"sourceId":    sourceID,
		"quote":       quote,
		"taskKey":     taskKey,
		"eventId":     eventID,
		"workspaceId": workspaceID,
		"title":       title,
		"phase":       phase,
		"description": description,
		"result":      result,
		"needsReview": needsReview,
	}
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
