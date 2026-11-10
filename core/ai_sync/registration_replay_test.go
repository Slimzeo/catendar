package ai_sync

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"catendar-wails/core/ai_sync/conversations"
	"catendar-wails/core/calendar"
)

type replayTestBoard struct {
	*testConversationBoard
	db      *sql.DB
	cards   []calendar.Event
	written []calendar.ChatChange
}

func (b *replayTestBoard) GetChatPlanningEvents(context.Context, string) ([]calendar.Event, error) {
	return b.cards, nil
}
func (b *replayTestBoard) ApplyChatPlan(ctx context.Context, day string, ids []string, changes []calendar.ChatChange) (calendar.ChatCounts, error) {
	if _, err := b.testConversationBoard.ApplyChatPlan(ctx, day, ids, changes); err != nil {
		return calendar.ChatCounts{}, err
	}
	for _, id := range ids {
		if _, err := b.db.ExecContext(ctx, `INSERT INTO chat_sources(source_id) VALUES (?)`, id); err != nil {
			return calendar.ChatCounts{}, err
		}
	}
	b.written = append(b.written, changes...)
	return calendar.ChatCounts{Updated: len(changes)}, nil
}

func TestUpgradeEnrichesOnlyLinkedRegistrationsAndRetriesWithoutReplayingPhase(t *testing.T) {
	s, base := newTestConversationSync(t)
	ctx := context.Background()
	now := time.Now()
	s.now = func() time.Time { return now }
	cards := []calendar.Event{{ID: 3, Title: "Workshop registration", SummaryKey: "workshop", Phase: "progressing"}, {ID: 4, Title: "Implement worker", Phase: "progressing"}, {ID: 5, Title: "Completed registration", Phase: "done"}}
	board := &replayTestBoard{testConversationBoard: base, db: s.db, cards: cards}
	s.calendar = board
	if _, err := s.db.Exec(`INSERT INTO chat_links VALUES('workshop-thread',3),('release-thread',4),('closed-thread',5); INSERT INTO chat_sources VALUES('old-form'),('build-report'),('finished-form'); UPDATE conversation_registration_backfills SET last_legacy_row=(SELECT MAX(rowid) FROM chat_sources) WHERE version=2; INSERT INTO chat_sources VALUES('after-upgrade')`); err != nil {
		t.Fatal(err)
	}
	history := []conversations.Message{{ID: "old-form", ThreadID: "workshop-thread", Text: "I am signing up for Orbit Jam", At: now.Unix() - 3600}, {ID: "build-report", ThreadID: "release-thread", Text: "deployed", At: now.Unix() - 3000}, {ID: "finished-form", ThreadID: "closed-thread", Text: "form closed", At: now.Unix() - 2500}, {ID: "after-upgrade", ThreadID: "workshop-thread", Text: "already handled", At: now.Unix()}}
	for _, m := range history {
		base.seen[m.ID] = true
	}
	s.read = func(context.Context, string, time.Time, time.Time) ([]conversations.Message, error) {
		return append([]conversations.Message(nil), history...), nil
	}
	attempts := 0
	s.plan = func(_ context.Context, _, _ string, events []calendar.Event, msgs []conversations.Message) ([]calendar.ChatChange, error) {
		attempts++
		if len(events) != 1 || events[0].ID != 3 {
			t.Fatalf("unexpected replay targets %+v", events)
		}
		if len(msgs) != 1 || msgs[0].ID != "old-form" || !msgs[0].RegistrationOnly {
			t.Fatalf("wrong history %+v", msgs)
		}
		if attempts == 1 {
			return nil, errors.New("retry")
		}
		m := msgs[0]
		link := []calendar.Registration{{Name: "Orbit Jam", URL: "https://orbit.example/apply"}}
		source := calendar.ChatEvidence{ID: m.ID, ThreadID: m.ThreadID, At: m.At, Quote: "signing up"}
		return []calendar.ChatChange{{Source: source, EventID: 3, TaskKey: "wrong_key", Title: "renamed", Description: "old description", Result: "old result", Phase: "done", Registrations: link}, {Source: source, EventID: 0, Title: "another project", Registrations: link}, {Source: source, EventID: 4, Title: "wrong card", Registrations: link}}, nil
	}
	config := s.Status().CodexPath
	if _, err := s.run(ctx, config, now); err == nil {
		t.Fatal("failed batch reported success")
	}
	if _, err := s.run(ctx, config, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.run(ctx, config, now); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(board.written) != 1 {
		t.Fatalf("calls=%d writes=%d", attempts, len(board.written))
	}
	got := board.written[0]
	if got.EventID != 3 || got.Title != cards[0].Title || got.Phase != "" || got.Result != "" || got.Description != "" || got.TaskKey != "workshop" || got.Source.ID != registrationCheckpoint("old-form") || len(got.Registrations) != 1 {
		t.Fatalf("replay was not additive: %+v", got)
	}
}

func TestRegistrationFollowupCanUseEarlierFormWithoutReplayingIt(t *testing.T) {
	history := []conversations.Message{{ID: "menu", ThreadID: "a", At: 1, Text: "Workshops registration: https://orbit.example/apply"}, {ID: "unrelated", ThreadID: "b", At: 2, Text: "Other registration https://random.example/apply"}, {ID: "small-talk", ThreadID: "a", At: 3, Text: "Thanks for yesterday."}, {ID: "decision", ThreadID: "a", At: 4, Text: "我决定报名 Orbit。"}}
	b := conversationBatches(history, map[string]bool{"decision": true})
	if len(b) != 1 || len(b[0]) != 3 || b[0][0].ID != "menu" || !b[0][0].ContextOnly || b[0][1].ID != "small-talk" || !b[0][1].ContextOnly || b[0][2].ContextOnly {
		t.Fatalf("registration follow-up lost its bounded same-thread context: %+v", b)
	}
}

func TestInterestUpgradeRevisitsAnUnlinkedSurveyOnce(t *testing.T) {
	s, base := newTestConversationSync(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	ctx := context.Background()
	// Both raw messages and a previous-version checkpoint predate this upgrade.
	if _, err := s.db.Exec(`INSERT INTO chat_sources VALUES('ask'),('listing'),(?); UPDATE conversation_registration_backfills SET last_legacy_row=(SELECT MAX(rowid) FROM chat_sources) WHERE version=2`, fmt.Sprintf("registration-v1:%x", sha256.Sum256([]byte("listing")))); err != nil {
		t.Fatal(err)
	}
	base.seen["ask"], base.seen["listing"] = true, true
	board := &replayTestBoard{testConversationBoard: base, db: s.db}
	s.calendar = board
	history := []conversations.Message{{ID: "ask", ThreadID: "survey", Role: conversations.RoleUser, Text: "帮我找近期可以参加的比赛，先比较备选。", At: now.Unix() - 100}, {ID: "listing", ThreadID: "survey", Role: conversations.RoleAssistant, Text: "North River 报名入口 https://example.org/north", At: now.Unix() - 90}}
	s.read = func(context.Context, string, time.Time, time.Time) ([]conversations.Message, error) {
		return append([]conversations.Message(nil), history...), nil
	}
	calls := 0
	s.plan = func(_ context.Context, _, _ string, cards []calendar.Event, msgs []conversations.Message) ([]calendar.ChatChange, error) {
		calls++
		if len(cards) != 0 || !msgs[0].RegistrationOnly {
			t.Fatal("expected candidate-only discovery")
		}
		return []calendar.ChatChange{{Source: calendar.ChatEvidence{ID: "listing", ThreadID: "survey", Quote: "North River 报名入口", At: now.Unix() - 90}, TaskKey: "north-river", Title: "活动报名清单", Phase: "done", Description: "old notes", Result: "submitted", Registrations: []calendar.Registration{{Name: "North River", URL: "https://example.org/north"}}}}, nil
	}
	for i := 0; i < 2; i++ {
		if _, err := s.run(ctx, s.Status().CodexPath, now); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || len(board.written) != 1 {
		t.Fatalf("calls=%d writes=%d", calls, len(board.written))
	}
	c := board.written[0]
	if c.Phase != calendar.PhaseTodo || c.Result != "" || c.Description != "" || c.TaskKey != "event-opportunities" || len(c.Registrations) != 1 {
		t.Fatalf("survey replay created a commitment: %+v", c)
	}
}

func TestRegistrationSurveyContextSurvivesAnInterveningTurn(t *testing.T) {
	msgs := []conversations.Message{{ID: "ask", ThreadID: "ours", Role: conversations.RoleUser, At: 1, Text: "请帮我看看有什么比赛"}, {ID: "other", ThreadID: "other", Role: conversations.RoleUser, At: 2, Text: "帮我找附近的会议"}, {ID: "aside", ThreadID: "ours", Role: conversations.RoleUser, At: 3, Text: "好的"}, {ID: "form", ThreadID: "ours", Role: conversations.RoleAssistant, At: 4, Text: "工作坊报名 https://example.org/register"}}
	b := conversationBatches(msgs, map[string]bool{"form": true})
	if len(b) != 1 || len(b[0]) != 3 || b[0][0].ID != "ask" || !b[0][0].ContextOnly || b[0][1].ID != "aside" || b[0][2].ContextOnly {
		t.Fatalf("wrong survey context %+v", b)
	}
}
