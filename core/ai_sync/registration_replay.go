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
const registrationUpgradeSQL = `CREATE TABLE IF NOT EXISTS conversation_registration_backfills (
 version INTEGER PRIMARY KEY, last_legacy_row INTEGER NOT NULL
);
INSERT OR IGNORE INTO conversation_registration_backfills SELECT 2,COALESCE(MAX(rowid),0) FROM chat_sources;`

type registrationBatch struct {
	messages []conversations.Message
	targets  *registrationTargets
}

// An explicitly requested event survey may have been skipped entirely by v1.
// Linked threads are restricted to their surviving cards during replay.
type registrationTargets struct {
	ids       map[int64]bool
	discovery bool
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
	return fmt.Sprintf("registration-v2:%x", sha256.Sum256([]byte(id)))
}

// Revisit checkpointed messages in the normal 72h window on manual sync.
// Versioned evidence IDs make a failed batch retryable without replaying phases.
func (s *ConversationSync) registrationBatches(ctx context.Context, day string, history []conversations.Message, fresh map[string]bool) ([]registrationBatch, error) {
	if len(history) == 0 || len(fresh) == len(history) {
		return nil, nil
	}
	var limit int64
	if err := s.db.QueryRowContext(ctx, `SELECT last_legacy_row FROM conversation_registration_backfills WHERE version=2`).Scan(&limit); err != nil {
		return nil, err
	}
	if limit == 0 {
		return nil, nil
	}
	cards, err := s.calendar.GetChatPlanningEvents(ctx, day)
	if err != nil {
		return nil, err
	}
	visible := make(map[int64]calendar.Event, len(cards))
	for _, card := range cards {
		visible[card.ID] = card
	}
	targets, err := s.registrationTargetsForHistory(ctx, history, visible)
	if err != nil || len(targets) == 0 {
		return nil, err
	}
	byThread := make(map[string][]conversations.Message)
	pending := make(map[string]bool)
	for _, m := range history {
		if targets[m.ThreadID] == nil {
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

// A formerly unlinked thread is eligible only with a user research request
// and actual candidate material. Do not revive a historic/deleted linked card.
func (s *ConversationSync) registrationTargetsForHistory(ctx context.Context, history []conversations.Message, visible map[int64]calendar.Event) (map[string]*registrationTargets, error) {
	type signals struct {
		survey  bool
		details bool
	}
	seen := map[string]signals{}
	for _, m := range history {
		f := seen[m.ThreadID]
		f.survey = f.survey || (m.Role == conversations.RoleUser && isEventSurvey(m.Text))
		f.details = f.details || isRegistrationEvidence(m.Text)
		seen[m.ThreadID] = f
	}
	targets := map[string]*registrationTargets{}
	for thread, flags := range seen {
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT event_id FROM chat_links WHERE thread_id=?`, thread)
		if err != nil {
			return nil, err
		}
		active := make(map[int64]bool)
		archived := false
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			card, ok := visible[id]
			if !ok || card.Phase == calendar.PhaseDone {
				archived = true
				continue
			}
			if registrationTopic(card) {
				active[id] = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if len(active) > 0 {
			targets[thread] = &registrationTargets{ids: active}
		} else if flags.survey && flags.details && !archived {
			targets[thread] = &registrationTargets{discovery: true}
		}
	}
	return targets, nil
}

func isEventSurvey(text string) bool {
	topic := strings.ToLower(text)
	return hasEventTopic(topic) && containsSurveyWord(topic, "找", "看看", "比较", "推荐", "整理", "收集", "想参加", "可以参加", "报名时间", "还有没有", "盘点", "筛选", "调研", "有没有", "find", "look for", "look up", "search", "compare", "research", "track", "collect", "shortlist", "which", "upcoming")
}

func hasEventTopic(topic string) bool {
	return containsSurveyWord(topic, "活动", "黑客松", "比赛", "大赛", "创业营", "加速营", "竞赛", "大会", "峰会", "工作坊", "会议", "hackathon", "contest", "conference", "workshop", "accelerator", "events", "competitions", "meetup", "competition", "summit")
}

func containsSurveyWord(text string, alternatives ...string) bool {
	for _, word := range alternatives {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func sortedRegistrationThreads(groups map[string][]conversations.Message) []string {
	threads := make([]string, 0, len(groups))
	for id := range groups {
		threads = append(threads, id)
	}
	sort.Strings(threads)
	return threads
}

// A replay writes registration evidence only. An unlinked survey can start a
// single todo list when there is no eligible card to enrich.
func registrationChanges(changes []calendar.ChatChange, cards []calendar.Event, allowNew bool) []calendar.ChatChange {
	byID := make(map[int64]calendar.Event, len(cards))
	for _, card := range cards {
		byID[card.ID] = card
	}
	result := make([]calendar.ChatChange, 0, len(changes))
	for _, change := range changes {
		card, ok := byID[change.EventID]
		if len(change.Registrations) == 0 || (!ok && !(allowNew && change.EventID == 0)) {
			continue
		}
		change.Source.ID = registrationCheckpoint(change.Source.ID)
		if ok {
			change.Title = card.Title
			if card.SummaryKey != "" {
				change.TaskKey = card.SummaryKey
			}
			change.Phase = ""
		} else {
			change.Phase = calendar.PhaseTodo
			change.TaskKey = "event-opportunities"
		}
		change.Description = ""
		change.Result = ""
		result = append(result, change)
	}
	return result
}

func isRegistrationEvidence(text string) bool {
	topic := strings.ToLower(text)
	links := strings.Count(topic, "https://") + strings.Count(topic, "http://")
	return (isRegistrationFollowup(topic) || hasEventTopic(topic) || links > 1 && strings.Contains(topic, "|")) &&
		(links > 0 || containsSurveyWord(topic, "截止", "deadline", "交作品", "封版"))
}
