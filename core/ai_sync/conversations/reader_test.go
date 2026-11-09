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
	"unicode/utf8"
)

func TestReadRecentCollectsOnlyPublicTextInWindow(t *testing.T) {
	base := time.Date(2026, time.November, 9, 12, 0, 0, 0, time.Local)
	since := time.Unix(base.Unix()-3600, 0)
	until := time.Unix(base.Unix()+3600, 0)

	activeThreads := []any{
		testThread(t, "t1", "Fix login flow", base.Unix()+3000, "/Users/alice/work/myproj", "cli"),
		testThread(t, "child", "Child agent", base.Unix()+2500, "/Users/alice/work/myproj", "cli"),
		testThread(t, "ephemeral", "Ephemeral run", base.Unix()+2400, "/tmp/scratch", "cli"),
		testThread(t, "exec", "Exec run", base.Unix()+2300, "/tmp/exec", "exec"),
		testThread(t, "catendar", "Catendar planner", base.Unix()+2200, "/tmp/catendar", "cli"),
		testThread(t, "old", "Old conversation", since.Unix()-900, "/tmp/old", "cli"),
	}
	// Exercise the reader's local defensive filters even if a server ignores
	// the sourceKinds request.
	activeThreads[1].(map[string]any)["parentThreadId"] = "t1"
	activeThreads[2].(map[string]any)["ephemeral"] = true
	activeThreads[4].(map[string]any)["originator"] = "catendar"

	fixture := fakeServerFixture{
		Threads: fakeServerThreads{
			Active: []json.RawMessage{
				mustRaw(t, map[string]any{
					"data":       activeThreads,
					"nextCursor": "threads-2",
				}),
				mustRaw(t, map[string]any{
					"data":       []any{testThread(t, "t9", "No messages", base.Unix()+2100, "/tmp/empty", "vscode")},
					"nextCursor": nil,
				}),
			},
			Archived: []json.RawMessage{
				mustRaw(t, map[string]any{
					"data": []any{
						testThread(t, "t2", "Archived plan", base.Unix()+2000, `C:\Users\bob\proj-b`, "vscode"),
					},
					"nextCursor": nil,
				}),
			},
		},
		Turns: map[string][]json.RawMessage{
			"t1": {
				mustRaw(t, map[string]any{
					"data": []any{
						map[string]any{
							"id":          "turn-user",
							"startedAt":   since.Unix() + 100,
							"completedAt": since.Unix() + 110,
							"status":      "completed",
							"items": []any{
								map[string]any{
									"type": "userMessage",
									"id":   "item-u1",
									"content": []any{
										map[string]any{
											"type": "text",
											"text": "<environment_context>\n<cwd>/Users/alice/private</cwd>\n</environment_context>\n请修复登录问题",
										},
										map[string]any{"type": "image", "text": "/Users/alice/private/screenshot.png"},
									},
								},
								map[string]any{"type": "reasoning", "id": "item-r1", "text": "hidden reasoning"},
								map[string]any{"type": "commandExecution", "id": "item-c1", "text": "command output"},
								map[string]any{"type": "agentMessage", "id": "item-a0", "phase": "commentary", "text": "Let me look"},
								map[string]any{"type": "agentMessage", "id": "item-a1", "phase": "final_answer", "text": "已完成后端修复"},
							},
						},
						map[string]any{
							"id":          "turn-2",
							"startedAt":   since.Unix() + 200,
							"completedAt": since.Unix() + 210,
							"status":      "inProgress",
							"items": []any{
								map[string]any{
									"type":    "userMessage",
									"id":      "item-u2",
									"content": []any{map[string]any{"type": "text", "text": "这个方案怎么样？"}},
								},
								map[string]any{"type": "agentMessage", "id": "item-a2", "phase": "final_answer", "text": "草稿内容"},
							},
						},
						map[string]any{
							"id":          "turn-3",
							"startedAt":   since.Unix() + 300,
							"completedAt": since.Unix() + 310,
							"status":      "completed",
							"items": []any{
								map[string]any{
									"type": "message",
									"id":   "item-old-u",
									"role": "user",
									"content": []any{
										map[string]any{"type": "text", "text": "旧格式用户"},
									},
								},
								map[string]any{
									"type": "message",
									"id":   "item-old-a",
									"role": "assistant",
									"text": "旧格式完成",
								},
							},
						},
					},
					"nextCursor": "turns-2",
				}),
				mustRaw(t, map[string]any{
					"data": []any{
						map[string]any{
							"id":        "turn-old",
							"timestamp": since.Unix() - 10,
							"items": []any{
								map[string]any{
									"type":    "userMessage",
									"id":      "item-old",
									"content": []any{map[string]any{"type": "text", "text": "旧请求"}},
								},
							},
						},
						map[string]any{
							"id": "turn-bad-time",
							"items": []any{
								map[string]any{
									"type":    "userMessage",
									"id":      "item-bad",
									"content": []any{map[string]any{"type": "text", "text": "无时间"}},
								},
							},
						},
					},
					"nextCursor": nil,
				}),
			},
			"t2": {
				mustRaw(t, map[string]any{
					"data": []any{
						map[string]any{
							"id":          "turn-archived",
							"startedAt":   since.Unix() + 2000,
							"completedAt": since.Unix() + 2010,
							"status":      "completed",
							"items": []any{
								map[string]any{
									"type":    "userMessage",
									"id":      "item-b1",
									"content": []any{map[string]any{"type": "text", "text": "  归档任务  "}},
								},
								map[string]any{"type": "agentMessage", "id": "item-b2", "phase": "", "text": "归档完成"},
							},
						},
					},
					"nextCursor": nil,
				}),
			},
		},
		Notifications: []json.RawMessage{
			json.RawMessage(`{"jsonrpc":"2.0","method":"account/rateLimits/updated","params":{"plan":"test"}}`),
		},
	}

	fixturePath := filepath.Join(t.TempDir(), "server.json")
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	if err := os.WriteFile(fixturePath, encoded, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	requestsPath := filepath.Join(t.TempDir(), "requests.jsonl")
	t.Setenv("CONVERSATIONS_TEST_HELPER", "server")
	t.Setenv("CONVERSATIONS_TEST_SERVER_FIXTURE", fixturePath)
	t.Setenv("CONVERSATIONS_TEST_REQUESTS_FILE", requestsPath)

	messages, err := ReadRecent(context.Background(), os.Args[0], since, until)
	if err != nil {
		t.Fatalf("read recent conversations: %v", err)
	}
	if len(messages) != 7 {
		t.Fatalf("unexpected message count: %d (%#v)", len(messages), messages)
	}
	expected := []struct {
		role    string
		text    string
		thread  string
		project string
		at      int64
	}{
		{RoleUser, "请修复登录问题", "t1", "myproj", since.Unix() + 100},
		{RoleAssistant, "已完成后端修复", "t1", "myproj", since.Unix() + 110},
		{RoleUser, "这个方案怎么样？", "t1", "myproj", since.Unix() + 200},
		{RoleUser, "旧格式用户", "t1", "myproj", since.Unix() + 300},
		{RoleAssistant, "旧格式完成", "t1", "myproj", since.Unix() + 310},
		{RoleUser, "归档任务", "t2", "proj-b", since.Unix() + 2000},
		{RoleAssistant, "归档完成", "t2", "proj-b", since.Unix() + 2010},
	}
	for index, want := range expected {
		got := messages[index]
		if got.Role != want.role || got.Text != want.text || got.ThreadID != want.thread || got.Project != want.project || got.At != want.at {
			t.Fatalf("message %d mismatch: %#v", index, got)
		}
		if len(got.ID) != 64 {
			t.Fatalf("message %d has unexpected id %q", index, got.ID)
		}
		if strings.Contains(got.Text, "/Users/alice/private") || strings.Contains(got.Text, "hidden") || strings.Contains(got.Text, "command output") {
			t.Fatalf("message %d leaked non-public content: %q", index, got.Text)
		}
	}
	if messages[0].ThreadTitle != "Fix login flow" || messages[5].ThreadTitle != "Archived plan" {
		t.Fatalf("unexpected thread titles: %#v", messages)
	}

	second, err := ReadRecent(context.Background(), os.Args[0], since, until)
	if err != nil {
		t.Fatalf("read recent conversations again: %v", err)
	}
	for index := range messages {
		if messages[index].ID != second[index].ID {
			t.Fatalf("message %d id is not deterministic", index)
		}
	}

	requests, err := os.ReadFile(requestsPath)
	if err != nil {
		t.Fatalf("read request log: %v", err)
	}
	assertReaderRequestParams(t, string(requests))
}

func assertReaderRequestParams(t *testing.T, log string) {
	t.Helper()
	archivedValues := make(map[bool]bool)
	turnThreads := make(map[string]bool)
	sawTurns := false
	for _, line := range strings.Split(log, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var request struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatalf("decode request log: %v", err)
		}
		switch request.Method {
		case "thread/list":
			var params struct {
				Limit       int      `json:"limit"`
				SortKey     string   `json:"sortKey"`
				SourceKinds []string `json:"sourceKinds"`
				Archived    bool     `json:"archived"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatalf("decode thread/list params: %v", err)
			}
			if params.Limit != threadPageSize || params.SortKey != "updated_at" {
				t.Fatalf("unexpected thread/list params: %#v", params)
			}
			if strings.Join(params.SourceKinds, ",") != "cli,vscode,appServer" {
				t.Fatalf("unexpected source kinds: %#v", params.SourceKinds)
			}
			archivedValues[params.Archived] = true
		case "thread/turns/list":
			sawTurns = true
			var params struct {
				ThreadID      string `json:"threadId"`
				SortDirection string `json:"sortDirection"`
				ItemsView     string `json:"itemsView"`
				Limit         int    `json:"limit"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatalf("decode turns params: %v", err)
			}
			if params.ThreadID == "" || params.SortDirection != "desc" || params.ItemsView != "full" || params.Limit != turnPageSize {
				t.Fatalf("unexpected thread/turns/list params: %#v", params)
			}
			turnThreads[params.ThreadID] = true
		}
	}
	if !archivedValues[false] || !archivedValues[true] {
		t.Fatalf("expected active and archived thread listing, got %#v", archivedValues)
	}
	if !sawTurns {
		t.Fatal("no thread/turns/list request was made")
	}
	if turnThreads["old"] {
		t.Fatal("reader fetched turns for a thread older than the window")
	}
}

func TestReadRecentReturnsEmptySliceForGenuinelyEmptySource(t *testing.T) {
	fixture := fakeServerFixture{}
	fixturePath := filepath.Join(t.TempDir(), "server.json")
	encoded, _ := json.Marshal(fixture)
	if err := os.WriteFile(fixturePath, encoded, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("CONVERSATIONS_TEST_HELPER", "server")
	t.Setenv("CONVERSATIONS_TEST_SERVER_FIXTURE", fixturePath)

	messages, err := ReadRecent(context.Background(), os.Args[0], time.Unix(1000, 0), time.Unix(2000, 0))
	if err != nil {
		t.Fatalf("read empty conversations: %v", err)
	}
	if messages == nil || len(messages) != 0 {
		t.Fatalf("expected a non-nil empty slice, got %#v", messages)
	}
}

func TestReadRecentDistinguishesUnsupportedSource(t *testing.T) {
	fixture := fakeServerFixture{Errors: map[string]fakeRPCError{
		"thread/list": {Code: -32601, Message: "method not found"},
	}}
	fixturePath := filepath.Join(t.TempDir(), "server.json")
	encoded, _ := json.Marshal(fixture)
	if err := os.WriteFile(fixturePath, encoded, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("CONVERSATIONS_TEST_HELPER", "server")
	t.Setenv("CONVERSATIONS_TEST_SERVER_FIXTURE", fixturePath)

	_, err := ReadRecent(context.Background(), os.Args[0], time.Unix(1000, 0), time.Unix(2000, 0))
	if !errors.Is(err, ErrSourceUnsupported) {
		t.Fatalf("expected unsupported source error, got %v", err)
	}
}

func TestReadRecentReportsMissingBinary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-codex")
	_, err := ReadRecent(context.Background(), missing, time.Unix(1000, 0), time.Unix(2000, 0))
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("expected unavailable source error, got %v", err)
	}
}

func TestReadRecentHonorsCancellation(t *testing.T) {
	fixture := fakeServerFixture{DelayMillis: 10000}
	fixturePath := filepath.Join(t.TempDir(), "server.json")
	encoded, _ := json.Marshal(fixture)
	if err := os.WriteFile(fixturePath, encoded, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("CONVERSATIONS_TEST_HELPER", "server")
	t.Setenv("CONVERSATIONS_TEST_SERVER_FIXTURE", fixturePath)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := ReadRecent(ctx, os.Args[0], time.Unix(1000, 0), time.Unix(2000, 0))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
}

func TestMessageDigestReprocessesEditedText(t *testing.T) {
	first := messageDigest("thread", "turn", "item", RoleUser, "原始内容")
	same := messageDigest("thread", "turn", "item", RoleUser, "原始内容")
	edited := messageDigest("thread", "turn", "item", RoleUser, "修改后的内容")
	if first != same {
		t.Fatal("message digest is not stable")
	}
	if first == edited {
		t.Fatal("message digest did not change after an edit")
	}
}

func TestCleanPublicTextRemovesInjectedEnvelopeAndKeepsChinese(t *testing.T) {
	value := "<environment_context>\n<cwd>/secret</cwd>\n</environment_context>\n请修复登录"
	if got := cleanPublicText(value); got != "请修复登录" {
		t.Fatalf("unexpected cleaned text: %q", got)
	}
	instructions := "# AGENTS.md instructions for /secret\n<INSTRUCTIONS>\nbe hidden\n</INSTRUCTIONS>\n真正的问题"
	if got := cleanPublicText(instructions); got != "真正的问题" {
		t.Fatalf("unexpected cleaned instructions: %q", got)
	}
}

func TestCleanPublicTextRemovesUIMetadataAndSecrets(t *testing.T) {
	value := "<collaboration_mode>plan</collaboration_mode>\n" +
		"<ide_context><ide_opened_file>/secret/file.go</ide_opened_file></ide_context>\n" +
		"<permissions instructions>never ask</permissions>\n" +
		"请修复登录，不要泄露 sk-" + strings.Repeat("a", 30) + " ghp_" + strings.Repeat("b", 32)
	got := cleanPublicText(value)
	if !strings.Contains(got, "请修复登录") {
		t.Fatalf("public Chinese text was lost: %q", got)
	}
	for _, leaked := range []string{"collaboration_mode", "/secret/file.go", "never ask", "sk-aaa", "ghp_bbb"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("cleaned text leaked %q: %q", leaked, got)
		}
	}

	assistant := cleanAssistantText("已完成 sk-" + strings.Repeat("a", 30))
	if strings.Contains(assistant, "sk-aaa") {
		t.Fatalf("assistant text leaked a secret: %q", assistant)
	}
}

func TestTruncateUTF8KeepsChineseValid(t *testing.T) {
	value := strings.Repeat("中", maxMessageBytes)
	truncated := truncateUTF8(value, maxMessageBytes)
	if len(truncated) > maxMessageBytes || !utf8.ValidString(truncated) {
		t.Fatalf("truncated text is invalid: bytes=%d", len(truncated))
	}
	if strings.ContainsRune(truncated, utf8.RuneError) {
		t.Fatal("truncated text contains a replacement character")
	}
}

func TestSafeProjectUsesBasenameOnly(t *testing.T) {
	cases := map[string]string{
		"/Users/alice/work/myproj": "myproj",
		`C:\Users\bob\proj-b`:      "proj-b",
		"/":                        "",
		".":                        "",
		"relative":                 "relative",
	}
	for input, want := range cases {
		if got := safeProject(input); got != want {
			t.Fatalf("safeProject(%q) = %q, want %q", input, got, want)
		}
	}
}

func testThread(t *testing.T, id, name string, updated int64, cwd, source string) map[string]any {
	t.Helper()
	return map[string]any{
		"id":             id,
		"name":           name,
		"updatedAt":      updated,
		"createdAt":      updated - 100,
		"cwd":            cwd,
		"ephemeral":      false,
		"parentThreadId": "",
		"source":         source,
		"originator":     "codex_cli_rs",
	}
}

func mustRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode raw fixture: %v", err)
	}
	return encoded
}
