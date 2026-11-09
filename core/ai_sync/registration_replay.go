package ai_sync

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"catendar-wails/core/ai_sync/conversations"
	"catendar-wails/core/calendar"
)

// Only messages already checkpointed by an older version can be replayed. A
// new installation captures zero; new messages use ordinary one-pass sync.
const registrationUpgradeSQL = `CREATE TABLE IF NOT EXISTS conversation_registration_upgrade (
 id INTEGER PRIMARY KEY CHECK(id=1), last_legacy_row INTEGER NOT NULL
);
INSERT OR IGNORE INTO conversation_registration_upgrade SELECT 1,COALESCE(MAX(rowid),0) FROM chat_sources;`

type registrationBatch struct {
	messages []conversations.Message
	targets  map[int64]bool
}

func registrationTopic(card calendar.Event) bool {
	if len(card.Registrations) > 0 {
		return true
	}
	return isRegistrationFollowup(card.Title + " " + card.SummaryKey + " " + card.Description)
}

func isRegistrationFollowup(text string) bool {
	topic := strings.ToLower(text)
	for _, word := range []string{"报名", "報名", "参赛", "參賽", "加速营", "黑客松", "申请", "申請", "registration", "register", "hackathon", "application", "apply", "sign up", "signup"} {
		if strings.Contains(topic, word) {
			return true
		}
	}
	return false
}

func registrationCheckpoint(id string) string {
	return fmt.Sprintf("registration-v1:%x", sha256.Sum256([]byte(id)))
}

// Revisit only the 72h window supplied by the manual sync, and only threads
// already linked to a visible registration card. Separate evidence IDs make a
// failed/cancelled batch retryable without replaying ordinary task decisions.
func (s *ConversationSync) registrationBatches(ctx context.Context, day string, history []conversations.Message, fresh map[string]bool) ([]registrationBatch, error) {
	if len(history) == 0 || len(fresh) == len(history) {
		return nil, nil
	}
	var limit int64
	if err := s.db.QueryRowContext(ctx, `SELECT last_legacy_row FROM conversation_registration_upgrade WHERE id=1`).Scan(&limit); err != nil {
		return nil, err
	}
	if limit == 0 {
		return nil, nil
	}
	cards, err := s.calendar.GetChatPlanningEvents(ctx, day)
	if err != nil {
		return nil, err
	}
	targets := make(map[string]map[int64]bool)
	for _, card := range cards {
		if card.Phase == calendar.PhaseDone || !registrationTopic(card) {
			continue
		}
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT thread_id FROM chat_links WHERE event_id=?`, card.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if targets[id] == nil {
				targets[id] = make(map[int64]bool)
			}
			targets[id][card.ID] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}
	byThread := make(map[string][]conversations.Message)
	pending := make(map[string]bool)
	for _, m := range history {
		if len(targets[m.ThreadID]) == 0 {
			continue
		}
		byThread[m.ThreadID] = append(byThread[m.ThreadID], m)
		if fresh[m.ID] {
			continue
		}
		var eligible bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_sources WHERE source_id=? AND rowid<=?) AND NOT EXISTS(SELECT 1 FROM chat_sources WHERE source_id=?)`, m.ID, limit, registrationCheckpoint(m.ID)).Scan(&eligible); err != nil {
			return nil, err
		}
		if eligible {
			pending[m.ID] = true
		}
	}
	var batches []registrationBatch
	for _, thread := range sortedRegistrationThreads(byThread) {
		for _, msgs := range conversationBatches(byThread[thread], pending) {
			for i := range msgs {
				msgs[i].RegistrationOnly = true
			}
			batches = append(batches, registrationBatch{msgs, targets[thread]})
		}
	}
	return batches, nil
}

func sortedRegistrationThreads(groups map[string][]conversations.Message) []string {
	threads := make([]string, 0, len(groups))
	for id := range groups {
		threads = append(threads, id)
	}
	sort.Strings(threads)
	return threads
}

// The migration may enrich a linked card, never add cards, replay an old
// phase/summary, or make an unrelated suggestion reviewable on today's board.
func registrationChanges(changes []calendar.ChatChange, cards []calendar.Event) []calendar.ChatChange {
	byID := make(map[int64]calendar.Event, len(cards))
	for _, card := range cards {
		byID[card.ID] = card
	}
	result := make([]calendar.ChatChange, 0, len(changes))
	for _, change := range changes {
		card, ok := byID[change.EventID]
		if !ok || len(change.Registrations) == 0 {
			continue
		}
		change.Source.ID = registrationCheckpoint(change.Source.ID)
		change.Title = card.Title
		if card.SummaryKey != "" {
			change.TaskKey = card.SummaryKey
		}
		change.Phase = ""
		change.Description = ""
		change.Result = ""
		result = append(result, change)
	}
	return result
}

func isRegistrationEvidence(text string) bool {
	return isRegistrationFollowup(text) && (strings.Contains(text, "https://") || strings.Contains(text, "http://") || strings.Contains(text, "截止") || strings.Contains(strings.ToLower(text), "deadline"))
}
