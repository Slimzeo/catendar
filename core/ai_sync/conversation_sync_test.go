package ai_sync

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"catendar-wails/core/ai_sync/conversations"
	"catendar-wails/core/calendar"
	_ "github.com/mattn/go-sqlite3"
)

type testConversationBoard struct {
	*calendar.Service
	seen map[string]bool
}

func (b *testConversationBoard) GetChatPlanningEvents(context.Context, string) ([]calendar.Event, error) {
	return nil, nil
}
func (b *testConversationBoard) ListChatActivity(context.Context) ([]calendar.ChatActivity, error) {
	return nil, nil
}
func (b *testConversationBoard) UnseenChatSources(_ context.Context, ids []string) (map[string]bool, error) {
	fresh := make(map[string]bool)
	for _, id := range ids {
		if !b.seen[id] {
			fresh[id] = true
		}
	}
	return fresh, nil
}
func (b *testConversationBoard) ApplyChatPlan(_ context.Context, _ string, ids []string, _ []calendar.ChatChange) (calendar.ChatCounts, error) {
	for _, id := range ids {
		if b.seen[id] {
			return calendar.ChatCounts{}, errors.New("attempted to apply context-only evidence")
		}
		b.seen[id] = true
	}
	return calendar.ChatCounts{}, nil
}

func newTestConversationSync(t *testing.T) (*ConversationSync, *testConversationBoard) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("CREATE TABLE ai_settings (id INTEGER PRIMARY KEY, codex_path TEXT)"); err != nil {
		t.Fatal(err)
	}
	board := &testConversationBoard{seen: make(map[string]bool)}
	s, err := NewConversationSync(db, board)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSettings(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	return s, board
}

func TestConversationSyncManualIncrementalAndRetry(t *testing.T) {
	s, board := newTestConversationSync(t)
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.Local)
	s.now = func() time.Time { return now }
	history := []conversations.Message{{ID: "first", ThreadID: "session", At: now.Add(-time.Hour).Unix(), Text: "Fix export"}}
	reads, calls := 0, 0
	s.read = func(_ context.Context, _ string, since, until time.Time) ([]conversations.Message, error) {
		if !since.Equal(now.Add(-72*time.Hour)) || !until.Equal(now) {
			t.Error("wrong history window")
		}
		reads++
		return append([]conversations.Message(nil), history...), nil
	}
	fail := false
	s.plan = func(_ context.Context, _, day string, _ []calendar.Event, messages []conversations.Message) ([]calendar.ChatChange, error) {
		calls++
		if day != "2026-11-09" {
			t.Error("wrong target day")
		}
		if calls >= 2 && (len(messages) != 2 || !messages[0].ContextOnly || messages[1].ContextOnly) {
			t.Errorf("followup lost its conversation context: %+v", messages)
		}
		if fail {
			return nil, errors.New("unavailable")
		}
		return nil, nil
	}
	ctx := context.Background()
	_ = s.Status()
	if _, err := s.Activity(ctx); err != nil {
		t.Fatal(err)
	}
	if reads != 0 || calls != 0 {
		t.Fatal("reading status or activity started a session scan")
	}
	run := func(state string) {
		t.Helper()
		if _, err := s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		s.wg.Wait()
		if got := s.Status(); got.State != state {
			t.Fatalf("unexpected sync report: %+v", got)
		}
	}
	run(StateSucceeded)
	run(StateSucceeded)
	if calls != 1 {
		t.Fatal("unchanged session invoked the model again")
	}
	history = append(history, conversations.Message{ID: "second", ThreadID: "session", At: now.Unix(), Text: "Finished"})
	fail = true
	run(StateFailed)
	if board.seen["second"] {
		t.Fatal("failed plan consumed its evidence")
	}
	fail = false
	run(StateSucceeded)
	if calls != 3 || !board.seen["second"] {
		t.Fatal("retry did not process the new evidence")
	}
}

func TestConversationSyncDuplicateStartAndCancellation(t *testing.T) {
	s, _ := newTestConversationSync(t)
	started := make(chan struct{})
	s.read = func(ctx context.Context, _ string, _, _ time.Time) ([]conversations.Message, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	first, err := s.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	<-started
	second, err := s.Start(context.Background())
	if err != nil || second.RunID != first.RunID {
		t.Fatalf("second click started another run: %+v, %v", second, err)
	}
	if !s.Cancel() {
		t.Fatal("active sync did not accept cancellation")
	}
	s.wg.Wait()
	if s.Status().State != StateCancelled {
		t.Fatalf("unexpected status: %+v", s.Status())
	}
}
