package ai_sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"catendar-wails/core/ai_sync/agent"
	"catendar-wails/core/ai_sync/conversations"
	"catendar-wails/core/calendar"
)

const (
	conversationTimeout = 90 * time.Minute
	conversationLease   = 2 * time.Minute
)

type ConversationStatus struct {
	State        string `json:"state"`
	Message      string `json:"message"`
	CodexPath    string `json:"codexPath"`
	RunID        string `json:"runId"`
	LastSyncedAt string `json:"lastSyncedAt"`
	Sources      int    `json:"sources"`
	Created      int    `json:"created"`
	Updated      int    `json:"updated"`
	Pending      int    `json:"pending"`
	Skipped      int    `json:"skipped"`
}

type conversationCalendar interface {
	GetChatPlanningEvents(context.Context, string) ([]calendar.Event, error)
	UnseenChatSources(context.Context, []string) (map[string]bool, error)
	ApplyChatPlan(context.Context, string, []string, []calendar.ChatChange) (calendar.ChatCounts, error)
	ListChatActivity(context.Context) ([]calendar.ChatActivity, error)
	ResolveChatChange(context.Context, int64, bool) (*calendar.ChatActivity, error)
}

type ConversationSync struct {
	db       *sql.DB
	calendar conversationCalendar
	now      func() time.Time
	read     func(context.Context, string, time.Time, time.Time) ([]conversations.Message, error)
	plan     func(context.Context, string, string, []calendar.Event, []conversations.Message) ([]calendar.ChatChange, error)

	mu     sync.Mutex
	status ConversationStatus
	cancel context.CancelFunc
	closed bool
	wg     sync.WaitGroup
}

func NewConversationSync(db *sql.DB, cal conversationCalendar) (*ConversationSync, error) {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS conversation_sync_settings (
		id INTEGER PRIMARY KEY CHECK(id=1),
		codex_path TEXT NOT NULL DEFAULT '', report TEXT NOT NULL DEFAULT '{}',
		lease TEXT NOT NULL DEFAULT '', lease_until INTEGER NOT NULL DEFAULT 0
	);
	INSERT OR IGNORE INTO conversation_sync_settings(id,codex_path)
	VALUES (1,COALESCE((SELECT codex_path FROM ai_settings WHERE id=1),''))`)
	if err != nil {
		return nil, err
	}
	s := &ConversationSync{db: db, calendar: cal, now: time.Now, read: conversations.ReadRecent,
		plan: conversations.Plan}
	var report, path string
	if err := db.QueryRow("SELECT codex_path,report FROM conversation_sync_settings WHERE id=1").Scan(&path, &report); err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(report), &s.status) != nil || s.status.State == "" || s.status.State == StateRunning {
		s.status = ConversationStatus{State: StateIdle}
	}
	s.status.CodexPath = path
	s.refreshPending()
	return s, nil
}

func (s *ConversationSync) Status() ConversationStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *ConversationSync) SaveSettings(ctx context.Context, path string) (ConversationStatus, error) {
	path = strings.TrimSpace(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.status, errors.New("catendar is closing")
	}
	if len(path) > 4096 || strings.ContainsRune(path, 0) {
		return s.status, errors.New("invalid Codex executable path")
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE conversation_sync_settings SET codex_path=? WHERE id=1", path); err != nil {
		return s.status, err
	}
	s.status.CodexPath = path
	return s.status, nil
}

// Only an explicit user action calls Start. Opening the app, reading its
// status or saving a path never accesses Codex conversation history.
func (s *ConversationSync) Start(parent context.Context) (ConversationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.status, errors.New("catendar is closing")
	}
	if s.cancel != nil {
		return s.status, nil
	}
	id, err := newRunID()
	if err != nil {
		return s.status, err
	}
	now := s.now()
	// Other instances may share this database. Only the lease owner spends
	// model calls; an interrupted process leaves a time-bounded lease.
	res, err := s.db.ExecContext(parent, `UPDATE conversation_sync_settings SET lease=?,lease_until=?
		WHERE id=1 AND (lease='' OR lease_until<=?)`, id, now.Add(conversationLease).Unix(), now.Unix())
	if err != nil {
		return s.status, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return s.status, err
	}
	if n != 1 {
		return s.status, errors.New("another catendar instance is syncing Codex conversations")
	}
	ctx, cancel := context.WithTimeout(parent, conversationTimeout)
	s.cancel = cancel
	s.status = ConversationStatus{State: StateRunning, Message: "Checking the last 72 hours of Codex conversations…",
		RunID: id, CodexPath: s.status.CodexPath, LastSyncedAt: s.status.LastSyncedAt, Pending: s.status.Pending}
	path := s.status.CodexPath
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// Renew only during this explicit run. A crashed app releases the lease
		// within two minutes, including during a long first sync.
		leaseDone := make(chan error, 1)
		go func() { leaseDone <- s.renewLease(ctx, id, cancel) }()
		message, runErr := s.run(ctx, path, now)
		cancel()
		if err := <-leaseDone; err != nil {
			runErr = err
		}
		s.finish(id, message, runErr)
	}()
	return s.status, nil
}

func (s *ConversationSync) renewLease(ctx context.Context, id string, cancel context.CancelFunc) error {
	ticker := time.NewTicker(conversationLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			result, err := s.db.ExecContext(ctx, "UPDATE conversation_sync_settings SET lease_until=? WHERE id=1 AND lease=?", s.now().Add(conversationLease).Unix(), id)
			if ctx.Err() != nil {
				return nil
			}
			if err == nil {
				if n, countErr := result.RowsAffected(); countErr == nil && n == 1 {
					continue
				}
			}
			cancel()
			return errors.New("the conversation sync lease expired; click AI Sync to continue")
		}
	}
}

func (s *ConversationSync) Cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return false
	}
	s.cancel()
	return true
}

func (s *ConversationSync) Shutdown() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *ConversationSync) Activity(ctx context.Context) ([]calendar.ChatActivity, error) {
	return s.calendar.ListChatActivity(ctx)
}

func (s *ConversationSync) Resolve(ctx context.Context, id int64, accept bool) (*calendar.ChatActivity, error) {
	row, err := s.calendar.ResolveChatChange(ctx, id, accept)
	if err == nil {
		s.refreshPending()
	}
	return row, err
}

func (s *ConversationSync) refreshPending() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chat_activities WHERE state='pending'").Scan(&count); err != nil {
		return
	}
	s.mu.Lock()
	s.status.Pending = count
	s.mu.Unlock()
}

func (s *ConversationSync) run(ctx context.Context, configuredPath string, now time.Time) (string, error) {
	adapter, _ := agent.ForProvider(agent.ProviderCodex)
	cli := agent.Resolve(adapter, configuredPath)
	if !cli.Installed {
		return "", fmt.Errorf("%s. Set the Codex executable in Settings → AI Sync", cli.Error)
	}
	messages, err := s.read(ctx, cli.Path, now.Add(-72*time.Hour), now)
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		ids = append(ids, m.ID)
	}
	unseen, err := s.calendar.UnseenChatSources(ctx, ids)
	if err != nil {
		return "", err
	}
	if len(unseen) == 0 {
		return "No new conversation messages in the last 72 hours.", nil
	}
	day := now.Format("2006-01-02")
	remaining := len(unseen)
	for _, batch := range conversationBatches(messages, unseen) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if s.now().Format("2006-01-02") != day {
			return "", errors.New("the local day changed; sync again for today's board")
		}
		cards, err := s.calendar.GetChatPlanningEvents(ctx, day)
		if err != nil {
			return "", err
		}
		s.mu.Lock()
		s.status.Message = fmt.Sprintf("Summarizing project progress · %d messages left", remaining)
		s.mu.Unlock()
		planCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		changes, err := s.plan(planCtx, cli.Path, day, cards, batch)
		cancel()
		if err != nil {
			return "", err
		}
		keys := make([]string, 0, len(batch))
		for _, m := range batch {
			if !m.ContextOnly {
				keys = append(keys, m.ID)
			}
		}
		if s.now().Format("2006-01-02") != day {
			return "", errors.New("the local day changed; sync again for today's board")
		}
		counts, err := s.calendar.ApplyChatPlan(ctx, day, keys, changes)
		if err != nil {
			return "", err
		}
		s.mu.Lock()
		s.status.Sources += len(keys)
		s.status.Created += counts.Created
		s.status.Updated += counts.Updated
		s.status.Skipped += counts.Skipped
		s.status.Pending += counts.Pending
		s.mu.Unlock()
		remaining -= len(keys)
	}
	return "Codex conversations synced.", nil
}

// A short reply can refer to an earlier message that was already synced. Supply
// the preceding turn from its thread as context, but checkpoint only fresh IDs.
func conversationBatches(messages []conversations.Message, unseen map[string]bool) [][]conversations.Message {
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].At < messages[j].At })
	var batches [][]conversations.Message
	var batch []conversations.Message
	size := 0
	previous := make(map[string]conversations.Message)
	inBatch := make(map[string]bool)
	for _, m := range messages {
		prior := previous[m.ThreadID]
		previous[m.ThreadID] = m
		if !unseen[m.ID] {
			continue
		}
		delete(unseen, m.ID)
		if len(batch) > 0 && (len(batch) >= 24 || size+len(m.Text) > 48000) {
			batches = append(batches, batch)
			batch, size, inBatch = nil, 0, make(map[string]bool)
		}
		if prior.ID != "" && !inBatch[prior.ID] && size+len(prior.Text)+len(m.Text) <= 48000 {
			prior.ContextOnly = true
			batch = append(batch, prior)
			size += len(prior.Text)
			inBatch[prior.ID] = true
		}
		batch = append(batch, m)
		size += len(m.Text)
		inBatch[m.ID] = true
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}

func (s *ConversationSync) finish(id, message string, runErr error) {
	s.refreshPending()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = nil
	s.status.State = StateSucceeded
	s.status.Message = message
	if runErr != nil {
		s.status.State = StateFailed
		s.status.Message = runErr.Error()
		if errors.Is(runErr, context.Canceled) {
			s.status.State = StateCancelled
			s.status.Message = "Conversation sync cancelled."
		}
	} else {
		s.status.LastSyncedAt = s.now().Format(time.RFC3339)
	}
	report, _ := json.Marshal(s.status)
	if _, err := s.db.Exec("UPDATE conversation_sync_settings SET report=?,lease='',lease_until=0 WHERE id=1 AND lease=?", string(report), id); err != nil {
		s.status.State = StateFailed
		s.status.Message = "Could not save the sync status: " + err.Error()
	}
}
