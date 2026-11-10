package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	chatStatePending   = "pending"
	chatStateApplied   = "applied"
	chatStateDismissed = "dismissed"

	maxChatSourceIDLen   = 200
	maxChatThreadIDLen   = 200
	maxChatThreadTitle   = 200
	maxChatQuoteLen      = 500
	maxChatTaskKeyLen    = 200
	maxChatTitleLen      = 200
	maxChatDescription   = 4000
	maxChatResult        = 4000
	maxChatChanges       = 200
	recentChatActivities = 25
)

var (
	errChatDoneWithoutResult = errors.New("a result is required before completing the task")
	errChatFinishedEarlier   = errors.New("a finished task from an earlier day cannot be created automatically")
)

const chatSchema = `
	CREATE TABLE IF NOT EXISTS chat_sources (
		source_id TEXT PRIMARY KEY,
		thread_id TEXT NOT NULL DEFAULT '',
		thread_title TEXT NOT NULL DEFAULT '',
		source_at INTEGER NOT NULL DEFAULT 0,
		quote TEXT NOT NULL DEFAULT '',
		task_key TEXT NOT NULL DEFAULT '',
		day TEXT NOT NULL DEFAULT '',
		seen_at DATETIME NOT NULL
	);
	CREATE TABLE IF NOT EXISTS chat_links (
		thread_id TEXT NOT NULL,
		task_key TEXT NOT NULL,
		event_id INTEGER NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		PRIMARY KEY (thread_id, task_key)
	);
	CREATE TABLE IF NOT EXISTS chat_activities (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		day TEXT NOT NULL,
		state TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		before_phase TEXT NOT NULL DEFAULT '',
		source_id TEXT NOT NULL,
		thread_id TEXT NOT NULL DEFAULT '',
		thread_title TEXT NOT NULL DEFAULT '',
		source_at INTEGER NOT NULL DEFAULT 0,
		quote TEXT NOT NULL DEFAULT '',
		task_key TEXT NOT NULL,
		event_id INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL,
		phase TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT '',
		registrations TEXT NOT NULL DEFAULT '[]',
		needs_review INTEGER NOT NULL DEFAULT 0,
		base_updated TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		resolved_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_chat_activities_state ON chat_activities(state, id);
	CREATE INDEX IF NOT EXISTS idx_chat_activities_source ON chat_activities(source_id);
	CREATE TABLE IF NOT EXISTS chat_summary_keys (
		event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
		task_key TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_chat_summary_keys_task_key ON chat_summary_keys(task_key);
	CREATE TABLE IF NOT EXISTS chat_registration_deadlines (
		event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
		derived TEXT
	);
	CREATE TABLE IF NOT EXISTS chat_ownership (
		event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
		manual_description INTEGER NOT NULL DEFAULT 0,
		manual_result INTEGER NOT NULL DEFAULT 0,
		manual_phase_at INTEGER NOT NULL DEFAULT 0,
		ai_description TEXT,
		ai_result TEXT,
		ai_phase TEXT,
		evidence_at INTEGER NOT NULL DEFAULT 0
	);
`

const chatActivityFields = `id, day, state, reason, before_phase,
	source_id, thread_id, thread_title, source_at, quote, task_key,
	event_id, title, phase, description, result, registrations,
	needs_review, base_updated`

// chatOwnership remembers what the assistant last wrote to an event. A field
// counts as AI-owned only while its current value still matches this baseline,
// so any human edit naturally transfers ownership back to the user.
type chatOwnership struct {
	manualDescription, manualResult bool
	manualPhaseAt                   int64
	description                     sql.NullString
	result                          sql.NullString
	phase                           sql.NullString
	evidenceAt                      int64
}

func (s *Service) UnseenChatSources(ctx context.Context, ids []string) (map[string]bool, error) {
	unseen := make(map[string]bool)
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := unseen[id]; ok {
			continue
		}
		var seen bool
		if err := s.db.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM chat_sources WHERE source_id = ?)", id,
		).Scan(&seen); err != nil {
			return nil, fmt.Errorf("check chat source: %w", err)
		}
		if !seen {
			unseen[id] = true
		}
	}
	return unseen, nil
}

// ApplyChatPlan consumes one analysis plan atomically. Every proposed change
// must reference one of the source IDs collected for this run and carry its
// thread, quote and timestamp; otherwise the whole plan is rejected before any
// source is marked seen or any task is touched.
func (s *Service) ApplyChatPlan(
	ctx context.Context,
	day string,
	sourceIDs []string,
	changes []ChatChange,
) (ChatCounts, error) {
	counts := ChatCounts{}
	day, err := normalizeBoardDay(day)
	if err != nil {
		return counts, err
	}
	now := s.now()
	if day != now.Format(dateLayout) {
		return counts, errors.New("chat plan day must match today's local date")
	}
	if len(changes) > maxChatChanges {
		return counts, fmt.Errorf("chat plan exceeds %d changes", maxChatChanges)
	}

	allowed := make(map[string]bool, len(sourceIDs))
	for _, raw := range sourceIDs {
		id := strings.TrimSpace(raw)
		if id != "" {
			allowed[id] = true
		}
	}
	for index, change := range changes {
		source := change.Source
		source.ID = strings.TrimSpace(source.ID)
		if source.ID == "" || !allowed[source.ID] {
			return counts, fmt.Errorf("chat proposal %d uses a source that is not part of this run", index+1)
		}
		if err := validateChatEvidence(source); err != nil {
			return counts, fmt.Errorf("chat proposal %d: %w", index+1, err)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return counts, fmt.Errorf("begin chat plan: %w", err)
	}
	defer tx.Rollback()

	evidence := make(map[string]ChatEvidence)
	taskKeys := make(map[string]string)
	for _, change := range changes {
		source := change.Source
		id := strings.TrimSpace(source.ID)
		if id == "" {
			continue
		}
		source.ID = id
		if _, ok := evidence[id]; !ok {
			evidence[id] = source
		}
		if key := strings.TrimSpace(change.TaskKey); key != "" && taskKeys[id] == "" {
			taskKeys[id] = key
		}
	}
	known, err := chatSeenSources(ctx, tx, sourceIDs)
	if err != nil {
		return counts, err
	}

	baselines := make(map[int64]time.Time)
	batchKeys := make(map[string]bool)
	for _, raw := range changes {
		change, err := normalizeChatChange(raw)
		if err != nil {
			return ChatCounts{}, err
		}
		if known[change.Source.ID] {
			counts.Skipped++
			continue
		}
		batchKey := change.Source.ThreadID + "\x00" + change.TaskKey
		// Several messages in one survey can contribute different activities
		// to the same existing card. Keep their evidence in this transaction.
		detailsOnly := change.EventID > 0 && len(change.Registrations) > 0 && change.Phase == "" && change.Description == "" && change.Result == ""
		if batchKeys[batchKey] && !detailsOnly {
			counts.Skipped++
			continue
		}
		batchKeys[batchKey] = true
		if err := s.applyChatChangeTx(ctx, tx, day, change, now, baselines, &counts); err != nil {
			return counts, err
		}
	}

	for _, raw := range sourceIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if err := recordChatSource(ctx, tx, id, evidence[id], taskKeys[id], day, now); err != nil {
			return counts, err
		}
	}
	if s.now().Format(dateLayout) != day {
		return counts, errors.New("chat plan crossed midnight; retry with today's date")
	}
	if err := tx.Commit(); err != nil {
		return counts, fmt.Errorf("commit chat plan: %w", err)
	}
	return counts, nil
}

func (s *Service) applyChatChangeTx(
	ctx context.Context,
	tx *sql.Tx,
	day string,
	change ChatChange,
	now time.Time,
	baselines map[int64]time.Time,
	counts *ChatCounts,
) error {
	target, ambiguous, reason, err := findChatTarget(ctx, tx, day, change)
	if err != nil {
		return err
	}
	if ambiguous {
		if err := s.insertChatActivity(ctx, tx, day, chatStatePending, reason, "", change, now); err != nil {
			return err
		}
		counts.Pending++
		return nil
	}
	if target == nil && reason != "" {
		// Never recreate a card whose supplied or linked identity is gone.
		counts.Skipped++
		return nil
	}
	if target == nil {
		if change.NeedsReview {
			if err := s.insertChatActivity(ctx, tx, day, chatStatePending, "the change is flagged for review", "", change, now); err != nil {
				return err
			}
			counts.Pending++
			return nil
		}
		if err := validateNewChatTask(change, day, now.Location()); err != nil {
			if errors.Is(err, errChatDoneWithoutResult) {
				if err := s.insertChatActivity(ctx, tx, day, chatStatePending, err.Error(), "", change, now); err != nil {
					return err
				}
				counts.Pending++
			} else {
				counts.Skipped++
			}
			return nil
		}
		event, err := createChatTaskTx(ctx, tx, day, &change, now)
		if err != nil {
			return err
		}
		change.EventID = event.ID
		baselines[event.ID] = event.UpdatedAt
		if err := s.insertChatActivity(ctx, tx, day, chatStateApplied, "", "", change, now); err != nil {
			return err
		}
		counts.Created++
		return nil
	}

	if _, ok := baselines[target.ID]; !ok {
		// Keep the version this run first saw. Later changes in the same plan
		// may legitimately build on this run's own preceding update.
		baselines[target.ID] = target.UpdatedAt
	}
	before, err := boardEvent(ctx, tx, target.ID, day, day)
	if errors.Is(err, sql.ErrNoRows) {
		counts.Skipped++
		return nil
	}
	if err != nil {
		return err
	}
	ownership, err := chatOwnershipByEvent(ctx, tx, target.ID)
	if err != nil {
		return err
	}
	if change.NeedsReview {
		pending := change
		pending.EventID = target.ID
		// Queue against the version the user will actually see, not the
		// proposal's possibly older analysis baseline.
		pending.BaseUpdated = target.UpdatedAt.Format(time.RFC3339Nano)
		if err := s.insertChatActivity(ctx, tx, day, chatStatePending, "the change is flagged for review", before.Phase, pending, now); err != nil {
			return err
		}
		counts.Pending++
		return nil
	}
	if reason := chatConflictReason(*target, before, change, ownership, baselines[target.ID]); reason != "" {
		pending := change
		pending.EventID = target.ID
		pending.BaseUpdated = target.UpdatedAt.Format(time.RFC3339Nano)
		if err := s.insertChatActivity(ctx, tx, day, chatStatePending, reason, before.Phase, pending, now); err != nil {
			return err
		}
		counts.Pending++
		return nil
	}
	if change.Title == before.Title && (change.Phase == "" || change.Phase == before.Phase) &&
		(change.Description == "" || change.Description == before.Description) &&
		(change.Result == "" || change.Result == before.Result) &&
		!registrationsWouldChange(before.Registrations, change.Registrations) {
		// A new session can repeat an already known outcome. Link its evidence
		// without rewriting the card or invalidating a pending manual review.
		if err := attachChatSummaryKey(ctx, tx, target.ID, change.TaskKey); err != nil {
			return err
		}
		if err := upsertChatLink(ctx, tx, change.Source.ThreadID, change.TaskKey, target.ID, now); err != nil {
			return err
		}
		counts.Skipped++
		return nil
	}
	if _, err := updateChatTaskTx(ctx, tx, day, target.ID, &change, now, false); err != nil {
		return err
	}
	if err := s.insertChatActivity(ctx, tx, day, chatStateApplied, "", before.Phase, change, now); err != nil {
		return err
	}
	counts.Updated++
	return nil
}

func normalizeChatChange(change ChatChange) (ChatChange, error) {
	source := change.Source
	source.ID = strings.TrimSpace(source.ID)
	source.ThreadID = strings.TrimSpace(source.ThreadID)
	source.ThreadTitle = strings.TrimSpace(source.ThreadTitle)
	source.Quote = strings.TrimSpace(source.Quote)
	if source.ID == "" {
		return ChatChange{}, errors.New("chat source id is required")
	}
	if len([]rune(source.ID)) > maxChatSourceIDLen {
		return ChatChange{}, fmt.Errorf("chat source id exceeds %d characters", maxChatSourceIDLen)
	}
	if len([]rune(source.ThreadID)) > maxChatThreadIDLen {
		return ChatChange{}, fmt.Errorf("chat thread id exceeds %d characters", maxChatThreadIDLen)
	}
	if len([]rune(source.ThreadTitle)) > maxChatThreadTitle {
		return ChatChange{}, fmt.Errorf("chat thread title exceeds %d characters", maxChatThreadTitle)
	}
	if len([]rune(source.Quote)) > maxChatQuoteLen {
		return ChatChange{}, fmt.Errorf("chat quote exceeds %d characters", maxChatQuoteLen)
	}
	if source.At < 0 {
		return ChatChange{}, errors.New("chat source time must not be negative")
	}
	change.Source = source

	change.TaskKey = strings.TrimSpace(change.TaskKey)
	if change.TaskKey == "" {
		return ChatChange{}, errors.New("chat task key is required")
	}
	if len([]rune(change.TaskKey)) > maxChatTaskKeyLen {
		return ChatChange{}, fmt.Errorf("chat task key exceeds %d characters", maxChatTaskKeyLen)
	}

	change.Title = strings.TrimSpace(change.Title)
	if change.Title == "" {
		return ChatChange{}, errors.New("chat title is required")
	}
	if len([]rune(change.Title)) > maxChatTitleLen {
		return ChatChange{}, fmt.Errorf("chat title exceeds %d characters", maxChatTitleLen)
	}

	phase := strings.TrimSpace(change.Phase)
	if phase != "" {
		validated, err := validatePhase(phase)
		if err != nil {
			return ChatChange{}, err
		}
		phase = validated
	}
	change.Phase = phase

	change.Description = strings.TrimSpace(change.Description)
	if len([]rune(change.Description)) > maxChatDescription {
		return ChatChange{}, fmt.Errorf("chat description exceeds %d characters", maxChatDescription)
	}
	change.Result = strings.TrimSpace(change.Result)
	if len([]rune(change.Result)) > maxChatResult {
		return ChatChange{}, fmt.Errorf("chat result exceeds %d characters", maxChatResult)
	}
	registrations, err := normalizeRegistrations(change.Registrations)
	if err != nil {
		return ChatChange{}, err
	}
	change.Registrations = registrations
	if change.EventID < 0 {
		return ChatChange{}, errors.New("chat event id must not be negative")
	}
	base := strings.TrimSpace(change.BaseUpdated)
	if base != "" {
		parsed, err := time.Parse(time.RFC3339Nano, base)
		if err != nil {
			return ChatChange{}, errors.New("chat baseUpdated must use RFC3339Nano")
		}
		base = parsed.Format(time.RFC3339Nano)
	}
	change.BaseUpdated = base
	return change, nil
}

func validateChatEvidence(source ChatEvidence) error {
	if strings.TrimSpace(source.ThreadID) == "" {
		return errors.New("chat source thread is required")
	}
	if strings.TrimSpace(source.Quote) == "" {
		return errors.New("chat source quote is required")
	}
	if source.At <= 0 {
		return errors.New("chat source timestamp is required")
	}
	return nil
}

func findChatTarget(ctx context.Context, tx *sql.Tx, day string, change ChatChange) (*Event, bool, string, error) {
	if change.EventID > 0 {
		event, err := eventByID(ctx, tx, change.EventID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, "the referenced task no longer exists", nil
		}
		if err != nil {
			return nil, false, "", err
		}
		if _, err := boardEvent(ctx, tx, event.ID, day, day); errors.Is(err, sql.ErrNoRows) {
			return nil, false, "the referenced task is not on today's board", nil
		} else if err != nil {
			return nil, false, "", err
		}
		return &event, false, "", nil
	}

	linkID, linked, err := chatLinkEventID(ctx, tx, change.Source.ThreadID, change.TaskKey)
	if err != nil {
		return nil, false, "", err
	}
	if linked {
		event, err := eventByID(ctx, tx, linkID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, "the linked task was deleted", nil
		}
		if err != nil {
			return nil, false, "", err
		}
		_, err = boardEvent(ctx, tx, event.ID, day, day)
		if err == nil {
			return &event, false, "", nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, "", err
		}
		// The earlier milestone exists but has left the board. A current
		// summary with the same key, possibly from another thread, takes over.

	}

	byKey, err := eventsBySummaryKey(ctx, tx, day, change.TaskKey)
	if err != nil {
		return nil, false, "", err
	}
	if len(byKey) > 1 {
		return nil, true, fmt.Sprintf("multiple tasks share the canonical key %q", change.TaskKey), nil
	}
	if len(byKey) == 1 {
		return &byKey[0], false, "", nil
	}

	matches, err := eventsByNormalizedTitle(ctx, tx, day, change.Title)
	if err != nil {
		return nil, false, "", err
	}
	if len(matches) > 1 {
		return nil, true, fmt.Sprintf("multiple tasks match %q", change.Title), nil
	}
	if len(matches) == 1 {
		return &matches[0], false, "", nil
	}
	return nil, false, "", nil
}

// eventsBySummaryKey resolves the stable task key across conversations. Only
// cards visible on the requested day may be matched, so a completed historical
// record or a future appointment is never silently retargeted.
func eventsBySummaryKey(ctx context.Context, tx *sql.Tx, day, taskKey string) ([]Event, error) {
	taskKey = strings.TrimSpace(taskKey)
	if taskKey == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT * FROM ("+boardSelect+`) WHERE id IN (
			SELECT event_id FROM chat_summary_keys WHERE task_key = ?3
		) ORDER BY id`, day, day, taskKey)
	if err != nil {
		return nil, fmt.Errorf("read chat canonical key candidates: %w", err)
	}
	return collectEvents(rows)
}

// eventsByNormalizedTitle is the last-resort match for cards that do not have
// a stable key yet. Cards with a different canonical key describe different
// tasks even when their titles happen to be identical. Only cards that are
// actually eligible on today's board are considered, so historical completed
// records and future appointments are never silently retargeted.
func eventsByNormalizedTitle(ctx context.Context, tx *sql.Tx, day, title string) ([]Event, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT * FROM (`+boardSelect+`) WHERE id NOT IN (
			SELECT event_id FROM chat_summary_keys
		) ORDER BY id`, day, day)
	if err != nil {
		return nil, fmt.Errorf("read chat title candidates: %w", err)
	}
	events, err := collectEvents(rows)
	if err != nil {
		return nil, err
	}
	normalized := normalizeChatTitle(title)
	matches := make([]Event, 0, 1)
	for _, event := range events {
		if normalizeChatTitle(event.Title) == normalized {
			matches = append(matches, event)
		}
	}
	return matches, nil
}

func normalizeChatTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

func chatConflictReason(current, before Event, change ChatChange, ownership chatOwnership, baseline time.Time) string {
	if change.BaseUpdated != "" {
		base, err := time.Parse(time.RFC3339Nano, change.BaseUpdated)
		if err == nil && !base.Equal(current.UpdatedAt) && (baseline.IsZero() || !base.Equal(baseline)) {
			return "the task changed after this conversation evidence was analyzed"
		}
	}

	if change.Title != current.Title || change.Title != before.Title {
		return "the suggestion would rename the current task"
	}
	phase := before.Phase
	if phase == "" {
		phase = current.Phase
	}
	if change.Phase != "" && change.Phase != phase && !allowsForwardPhase(phase, change.Phase) {
		return fmt.Sprintf("moving from %s to %s would reopen or regress the task", phase, change.Phase)
	}

	if change.Phase != "" && change.Phase != phase {
		if ownership.manualPhaseAt > chatEvidenceMillis(change.Source.At) {
			return "the task phase was changed manually after the conversation"
		}
		if ownership.phase.Valid && ownership.phase.String == phase && ownership.evidenceAt > chatEvidenceMillis(change.Source.At) {
			return "the current phase has more recent evidence"
		}
	}
	if ownership.manualDescription && change.Description != "" && change.Description != before.Description {
		return "the suggestion would overwrite a manually edited description"
	}
	if ownership.manualResult && change.Result != "" && change.Result != before.Result {
		return "the suggestion would overwrite a manually edited result"
	}
	description := strings.TrimSpace(change.Description)
	if description != "" {
		if reason := chatFieldConflict("description", description,
			before.Description, current.Description,
			ownership.description, ownership.evidenceAt, change.Source.At); reason != "" {
			return reason
		}
	}
	result := strings.TrimSpace(change.Result)
	if result != "" {
		if reason := chatFieldConflict("result", result,
			before.Result, current.Result,
			ownership.result, ownership.evidenceAt, change.Source.At); reason != "" {
			return reason
		}
	}
	for _, existing := range [][]Registration{before.Registrations, current.Registrations} {
		if reason := registrationConflictReason(existing, change.Registrations); reason != "" {
			return reason
		}
	}

	if change.Phase == PhaseDone && result == "" {
		return errChatDoneWithoutResult.Error()
	}
	return ""
}

func chatFieldConflict(field, proposed, beforeValue, currentValue string, aiValue sql.NullString, evidenceAt, sourceAt int64) string {
	for _, existing := range []string{beforeValue, currentValue} {
		existing = strings.TrimSpace(existing)
		if existing == "" || existing == proposed {
			continue
		}
		if aiValue.Valid && aiValue.String == existing {
			if chatEvidenceMillis(sourceAt) > evidenceAt {
				continue
			}
			return fmt.Sprintf("the suggestion is not newer than the last automated %s update", field)
		}
		return fmt.Sprintf("the suggestion would overwrite a manually written %s", field)
	}
	return ""
}

func allowsForwardPhase(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case PhaseTodo:
		return to == PhaseProgressing || to == PhaseDone
	case PhaseProgressing:
		return to == PhaseDone
	default:
		return false
	}
}

func validateNewChatTask(change ChatChange, day string, location *time.Location) error {
	if change.Phase != PhaseDone {
		return nil
	}
	if change.Result == "" {
		return errChatDoneWithoutResult
	}
	sourceDay := chatSourceDay(change.Source.At, location)
	if sourceDay == "" || sourceDay < day {
		return errChatFinishedEarlier
	}
	return nil
}

// chatEvidenceMillis normalizes both second and millisecond timestamps so
// ordering comparisons stay meaningful across evidence providers.
func chatEvidenceMillis(at int64) int64 {
	if at <= 0 {
		return 0
	}
	if at > 1_000_000_000_000 {
		return at
	}
	return at * 1000
}

func chatSourceDay(at int64, location *time.Location) string {
	millis := chatEvidenceMillis(at)
	if millis == 0 {
		return ""
	}
	return time.UnixMilli(millis).In(location).Format(dateLayout)
}

func createChatTaskTx(ctx context.Context, tx *sql.Tx, day string, change *ChatChange, now time.Time) (Event, error) {
	if err := validateNewChatTask(*change, day, now.Location()); err != nil {
		return Event{}, err
	}
	phase := change.Phase
	if phase == "" {
		phase = PhaseTodo
	}
	input := EventInput{
		Title:       change.Title,
		Start:       day + "T00:00:00",
		End:         day + "T00:00:00",
		Color:       defaultEventColor,
		AllDay:      true,
		Description: change.Description,
		Phase:       phase,
	}
	if change.Result != "" {
		result := change.Result
		input.Result = &result
	}
	event, err := createEventTx(ctx, tx, input, now)
	if err != nil {
		return Event{}, err
	}
	if len(change.Registrations) > 0 {
		event.Registrations, _ = mergeRegistrations(nil, change.Registrations, false)
		event.Deadline = deriveRegistrationDeadline(event.Registrations, now.Format(dateLayout))
		if event.Deadline != "" {
			if err := recordRegistrationDeadline(ctx, tx, event.ID, event.Deadline); err != nil {
				return Event{}, err
			}
		}
		if err := writeRegistrationDetailsTx(ctx, tx, &event, now); err != nil {
			return Event{}, err
		}
	}
	change.EventID = event.ID
	change.Phase = phase
	if err := attachChatSummaryKey(ctx, tx, event.ID, change.TaskKey); err != nil {
		return Event{}, err
	}
	if err := recordChatOwnership(ctx, tx, event.ID, *change); err != nil {
		return Event{}, err
	}
	if err := upsertChatLink(ctx, tx, change.Source.ThreadID, change.TaskKey, event.ID, now); err != nil {
		return Event{}, err
	}
	return event, nil
}

func updateChatTaskTx(ctx context.Context, tx *sql.Tx, day string, eventID int64, change *ChatChange, now time.Time, overwriteRegistrations bool) (Event, error) {
	written := *change
	var owned sql.NullString
	var tracked bool
	if len(change.Registrations) > 0 {
		err := tx.QueryRowContext(ctx, "SELECT derived FROM chat_registration_deadlines WHERE event_id=?", eventID).Scan(&owned)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Event{}, err
		}
		tracked = err == nil
	}
	derived := false
	updated, err := changeEventTx(ctx, tx, eventID, day, now, func(event *Event) {
		if event.Description == change.Description {
			written.Description = ""
		}
		if event.Result == change.Result {
			written.Result = ""
		}
		if event.Phase == change.Phase {
			written.Phase = ""
		}
		event.Title = change.Title
		if change.Phase != "" {
			event.Phase = change.Phase
		}
		if change.Description != "" {
			event.Description = change.Description
		}
		if change.Result != "" {
			event.Result = change.Result
		}
		merged, registrationsChanged := mergeRegistrations(event.Registrations, change.Registrations, overwriteRegistrations)
		event.Registrations = merged
		if registrationsChanged && ((!tracked && event.Deadline == "") || (owned.Valid && event.Deadline == owned.String)) {
			event.Deadline = deriveRegistrationDeadline(merged, now.Format(dateLayout))
			derived = event.Deadline != ""
		}
	})
	if err != nil {
		return Event{}, err
	}
	if derived {
		if err := recordRegistrationDeadline(ctx, tx, eventID, updated.Deadline); err != nil {
			return Event{}, err
		}
	}
	change.EventID = eventID
	if err := attachChatSummaryKey(ctx, tx, eventID, change.TaskKey); err != nil {
		return Event{}, err
	}
	if err := recordChatOwnership(ctx, tx, eventID, written); err != nil {
		return Event{}, err
	}
	if err := upsertChatLink(ctx, tx, change.Source.ThreadID, change.TaskKey, eventID, now); err != nil {
		return Event{}, err
	}
	return updated, nil
}

// attachChatSummaryKey records the first stable key for an event. INSERT OR
// IGNORE keeps an existing canonical key even when a later conversation uses a
// different local task key for the same card.
func attachChatSummaryKey(ctx context.Context, tx *sql.Tx, eventID int64, taskKey string) error {
	taskKey = strings.TrimSpace(taskKey)
	if eventID <= 0 || taskKey == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO chat_summary_keys (event_id, task_key) VALUES (?, ?)",
		eventID, taskKey,
	); err != nil {
		return fmt.Errorf("record chat summary key: %w", err)
	}
	return nil
}

// GetChatPlanningEvents returns the same cards as the daily board together with
// their canonical task keys so the planner can reuse a stable identity across
// conversations and threads.
func (s *Service) GetChatPlanningEvents(ctx context.Context, day string) ([]Event, error) {
	day, err := normalizeBoardDay(day)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT v.*, COALESCE(k.task_key, '') FROM (`+
		boardSelect+`) v LEFT JOIN chat_summary_keys k ON k.event_id=v.id ORDER BY v.start, v.id`,
		day, s.now().Format(dateLayout))
	if err != nil {
		return nil, fmt.Errorf("read current summary cards: %w", err)
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		var key string
		event, err := scanEvent(rows, &key)
		if err != nil {
			return nil, err
		}
		event.SummaryKey = key
		events = append(events, event)
	}
	return events, rows.Err()
}

func chatSeenSources(ctx context.Context, tx *sql.Tx, ids []string) (map[string]bool, error) {
	seen := make(map[string]bool)
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM chat_sources WHERE source_id = ?)", id,
		).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check chat source: %w", err)
		}
		if exists {
			seen[id] = true
		}
	}
	return seen, nil
}

func recordChatSource(
	ctx context.Context,
	tx *sql.Tx,
	id string,
	source ChatEvidence,
	taskKey, day string,
	now time.Time,
) error {
	if id == "" {
		return nil
	}
	threadTitle := strings.TrimSpace(source.ThreadTitle)
	if len([]rune(threadTitle)) > maxChatThreadTitle {
		threadTitle = string([]rune(threadTitle)[:maxChatThreadTitle])
	}
	quote := strings.TrimSpace(source.Quote)
	if len([]rune(quote)) > maxChatQuoteLen {
		quote = string([]rune(quote)[:maxChatQuoteLen])
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO chat_sources (
			source_id, thread_id, thread_title, source_at, quote, task_key, day, seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, strings.TrimSpace(source.ThreadID), threadTitle, source.At, quote, taskKey, day, now); err != nil {
		return fmt.Errorf("record chat source: %w", err)
	}
	return nil
}

// Record direct edits separately so an older conversation cannot undo an
// intentional phase change or refill a field the user explicitly cleared.
func recordManualChatEdit(ctx context.Context, tx *sql.Tx, before, after Event, now time.Time) error {
	if before.Deadline != after.Deadline {
		if err := recordRegistrationDeadline(ctx, tx, after.ID, nil); err != nil {
			return err
		}
	}
	description := before.Description != after.Description
	result := before.Result != after.Result
	var phaseAt int64
	if before.Phase != after.Phase {
		phaseAt = now.UnixMilli()
	}
	if !description && !result && phaseAt == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO chat_ownership (event_id, manual_description, manual_result, manual_phase_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(event_id) DO UPDATE SET
			ai_description = CASE WHEN excluded.manual_description THEN NULL ELSE ai_description END,
			ai_result = CASE WHEN excluded.manual_result THEN NULL ELSE ai_result END,
			ai_phase = CASE WHEN excluded.manual_phase_at != 0 THEN NULL ELSE ai_phase END,
			manual_description = MAX(manual_description, excluded.manual_description),
			manual_result = MAX(manual_result, excluded.manual_result),
			manual_phase_at = MAX(manual_phase_at, excluded.manual_phase_at)
	`, after.ID, description, result, phaseAt)
	return err
}

func chatOwnershipByEvent(ctx context.Context, q eventReader, eventID int64) (chatOwnership, error) {
	var ownership chatOwnership
	err := q.QueryRowContext(ctx, `
		SELECT ai_description, ai_result, ai_phase, evidence_at, manual_description, manual_result, manual_phase_at
		FROM chat_ownership WHERE event_id = ?
	`, eventID).Scan(&ownership.description, &ownership.result, &ownership.phase, &ownership.evidenceAt, &ownership.manualDescription, &ownership.manualResult, &ownership.manualPhaseAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ownership, nil
	}
	if err != nil {
		return chatOwnership{}, fmt.Errorf("read chat ownership: %w", err)
	}
	return ownership, nil
}

// recordChatOwnership stores only the fields this change actually wrote. Omitted
// fields keep their previous baseline so a later proposal cannot mistake the
// surrounding manual value for an AI-owned one.
func recordChatOwnership(ctx context.Context, tx *sql.Tx, eventID int64, change ChatChange) error {
	var description, result, phase any
	if change.Description != "" {
		description = change.Description
	}
	if change.Result != "" {
		result = change.Result
	}
	if change.Phase != "" {
		phase = change.Phase
	}
	if description == nil && result == nil && phase == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_ownership (event_id, ai_description, ai_result, ai_phase, evidence_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(event_id) DO UPDATE SET
			ai_description = COALESCE(excluded.ai_description, chat_ownership.ai_description),
			ai_result = COALESCE(excluded.ai_result, chat_ownership.ai_result),
			ai_phase = COALESCE(excluded.ai_phase, chat_ownership.ai_phase),
			manual_description = CASE WHEN excluded.ai_description IS NULL THEN manual_description ELSE 0 END,
			manual_result = CASE WHEN excluded.ai_result IS NULL THEN manual_result ELSE 0 END,
			manual_phase_at = CASE WHEN excluded.ai_phase IS NULL THEN manual_phase_at ELSE 0 END,
			evidence_at = MAX(evidence_at, excluded.evidence_at)
	`, eventID, description, result, phase, chatEvidenceMillis(change.Source.At)); err != nil {
		return fmt.Errorf("record chat ownership: %w", err)
	}
	return nil
}

func chatLinkEventID(ctx context.Context, tx *sql.Tx, threadID, taskKey string) (int64, bool, error) {
	var eventID int64
	err := tx.QueryRowContext(ctx,
		"SELECT event_id FROM chat_links WHERE thread_id = ? AND task_key = ?", threadID, taskKey,
	).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read chat link: %w", err)
	}
	return eventID, true, nil
}

func upsertChatLink(ctx context.Context, tx *sql.Tx, threadID, taskKey string, eventID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_links (thread_id, task_key, event_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(thread_id, task_key) DO UPDATE SET
			event_id = excluded.event_id, updated_at = excluded.updated_at
	`, threadID, taskKey, eventID, now, now); err != nil {
		return fmt.Errorf("record chat link: %w", err)
	}
	return nil
}

func (s *Service) insertChatActivity(
	ctx context.Context,
	tx *sql.Tx,
	day, state, reason, beforePhase string,
	change ChatChange,
	now time.Time,
) error {
	var resolved any
	if state != chatStatePending {
		resolved = now
	}
	needsReview := 0
	if change.NeedsReview {
		needsReview = 1
	}
	registrations, err := encodeRegistrations(change.Registrations)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_activities (
			day, state, reason, before_phase, source_id, thread_id, thread_title,
			source_at, quote, task_key, event_id, title, phase,
			description, result, registrations, needs_review, base_updated, created_at, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, day, state, reason, beforePhase, change.Source.ID, change.Source.ThreadID, change.Source.ThreadTitle,
		change.Source.At, change.Source.Quote, change.TaskKey, change.EventID,
		change.Title, change.Phase, change.Description, change.Result, registrations, needsReview, change.BaseUpdated,
		now, resolved); err != nil {
		return fmt.Errorf("record chat activity: %w", err)
	}
	return nil
}

func (s *Service) ListChatActivity(ctx context.Context) ([]ChatActivity, error) {
	pending, err := s.chatActivities(ctx,
		"SELECT "+chatActivityFields+" FROM chat_activities WHERE state = ? ORDER BY id DESC",
		chatStatePending)
	if err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		today := s.now().Format(dateLayout)
		cards, err := s.GetBoardEvents(ctx, today)
		if err != nil {
			return nil, err
		}
		current := make(map[int64]*Event, len(cards))
		for i := range cards {
			current[cards[i].ID] = &cards[i]
		}
		for i := range pending {
			if pending[i].Day == today {
				pending[i].Current = current[pending[i].Change.EventID]
			}
		}
	}
	resolved, err := s.chatActivities(ctx,
		"SELECT "+chatActivityFields+` FROM chat_activities
		WHERE state <> ? ORDER BY id DESC LIMIT ?`,
		chatStatePending, recentChatActivities)
	if err != nil {
		return nil, err
	}
	return append(pending, resolved...), nil
}

func (s *Service) chatActivities(ctx context.Context, query string, args ...any) ([]ChatActivity, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list chat activity: %w", err)
	}
	defer rows.Close()
	activities := make([]ChatActivity, 0)
	for rows.Next() {
		activity, err := scanChatActivity(rows)
		if err != nil {
			return nil, err
		}
		activities = append(activities, activity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read chat activity: %w", err)
	}
	return activities, nil
}

func scanChatActivity(row rowScanner) (ChatActivity, error) {
	var activity ChatActivity
	var needsReview int
	var registrations string
	if err := row.Scan(
		&activity.ID, &activity.Day, &activity.State, &activity.Reason, &activity.BeforePhase,
		&activity.Change.Source.ID, &activity.Change.Source.ThreadID, &activity.Change.Source.ThreadTitle,
		&activity.Change.Source.At, &activity.Change.Source.Quote, &activity.Change.TaskKey,
		&activity.Change.EventID, &activity.Change.Title,
		&activity.Change.Phase, &activity.Change.Description, &activity.Change.Result,
		&registrations, &needsReview, &activity.Change.BaseUpdated,
	); err != nil {
		return ChatActivity{}, fmt.Errorf("scan chat activity: %w", err)
	}
	decoded, err := decodeRegistrations(registrations)
	if err != nil {
		return ChatActivity{}, err
	}
	activity.Change.Registrations = decoded
	activity.Change.NeedsReview = needsReview == 1
	return activity, nil
}

func (s *Service) ResolveChatChange(ctx context.Context, id int64, accept bool) (*ChatActivity, error) {
	if id <= 0 {
		return nil, errors.New("chat activity id is required")
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin chat resolution: %w", err)
	}
	defer tx.Rollback()
	activity, err := chatActivityByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if activity.State != chatStatePending {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit chat resolution: %w", err)
		}
		return &activity, nil
	}

	if !accept {
		if err := dismissChatActivity(ctx, tx, &activity, "dismissed by user", now); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit chat dismissal: %w", err)
		}
		return &activity, nil
	}

	if err := recordChatSource(ctx, tx, activity.Change.Source.ID, activity.Change.Source,
		activity.Change.TaskKey, activity.Day, now); err != nil {
		return nil, err
	}
	if err := s.acceptChatActivityTx(ctx, tx, &activity, now); err != nil {
		return nil, err
	}
	resolved, err := chatActivityByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit chat resolution: %w", err)
	}
	return &resolved, nil
}

func (s *Service) acceptChatActivityTx(ctx context.Context, tx *sql.Tx, activity *ChatActivity, now time.Time) error {
	change := activity.Change
	if activity.Day != now.Format(dateLayout) {
		return dismissChatActivity(ctx, tx, activity, "this suggestion is from an earlier day", now)
	}

	target, ambiguous, reason, err := findChatTarget(ctx, tx, activity.Day, change)
	if err != nil {
		return err
	}
	if ambiguous {
		return dismissChatActivity(ctx, tx, activity, reason, now)
	}
	if target == nil && reason != "" {
		return dismissChatActivity(ctx, tx, activity, reason, now)
	}
	if target == nil {
		if err := validateNewChatTask(change, activity.Day, now.Location()); err != nil {
			return dismissChatActivity(ctx, tx, activity, err.Error(), now)
		}
		if _, err := createChatTaskTx(ctx, tx, activity.Day, &change, now); err != nil {
			return err
		}
		return approveChatActivity(ctx, tx, activity, change, now)
	}

	if change.BaseUpdated != "" {
		base, err := time.Parse(time.RFC3339Nano, change.BaseUpdated)
		if err != nil || !base.Equal(target.UpdatedAt) {
			return dismissChatActivity(ctx, tx, activity, "the task changed after this suggestion was created", now)
		}
	}
	_, err = boardEvent(ctx, tx, target.ID, activity.Day, activity.Day)
	if errors.Is(err, sql.ErrNoRows) {
		return dismissChatActivity(ctx, tx, activity, "the task is no longer on today's board", now)
	}
	if err != nil {
		return err
	}
	if change.Phase == PhaseDone && strings.TrimSpace(change.Result) == "" {
		return dismissChatActivity(ctx, tx, activity, errChatDoneWithoutResult.Error(), now)
	}
	if _, err := updateChatTaskTx(ctx, tx, activity.Day, target.ID, &change, now, true); err != nil {
		return err
	}
	return approveChatActivity(ctx, tx, activity, change, now)
}

func approveChatActivity(ctx context.Context, tx *sql.Tx, activity *ChatActivity, change ChatChange, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE chat_activities SET state = ?, reason = ?, event_id = ?,
			base_updated = ?, resolved_at = ? WHERE id = ? AND state = ?
	`, chatStateApplied, "approved by user", change.EventID,
		change.BaseUpdated, now, activity.ID, chatStatePending)
	if err != nil {
		return fmt.Errorf("approve chat activity: %w", err)
	}
	activity.State = chatStateApplied
	activity.Reason = "approved by user"
	activity.Change = change
	return nil
}

func dismissChatActivity(ctx context.Context, tx *sql.Tx, activity *ChatActivity, reason string, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"UPDATE chat_activities SET state = ?, reason = ?, resolved_at = ? WHERE id = ? AND state = ?",
		chatStateDismissed, reason, now, activity.ID, chatStatePending,
	); err != nil {
		return fmt.Errorf("dismiss chat activity: %w", err)
	}
	activity.State = chatStateDismissed
	activity.Reason = reason
	return nil
}

func chatActivityByID(ctx context.Context, tx *sql.Tx, id int64) (ChatActivity, error) {
	activity, err := scanChatActivity(tx.QueryRowContext(ctx,
		"SELECT "+chatActivityFields+" FROM chat_activities WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return ChatActivity{}, sql.ErrNoRows
	}
	if err != nil {
		return ChatActivity{}, err
	}
	return activity, nil
}
