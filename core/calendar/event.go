package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	defaultEventColor = "#7663b5"
	maxBatchSize      = 50

	PhaseTodo        = "todo"
	PhaseProgressing = "progressing"
	PhaseDone        = "done"

	dateLayout = "2006-01-02"
)

var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type Event struct {
	ID            int64          `json:"id"`
	Title         string         `json:"title"`
	Start         string         `json:"start"`
	End           string         `json:"end,omitempty"`
	Color         string         `json:"color"`
	AllDay        bool           `json:"allDay"`
	Description   string         `json:"description"`
	Result        string         `json:"result,omitempty"`
	Bold          bool           `json:"bold"`
	Phase         string         `json:"phase"`
	Deadline      string         `json:"deadline,omitempty"`
	Registrations []Registration `json:"-"`
	SummaryKey    string         `json:"-"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

type EventInput struct {
	Title       string  `json:"title"`
	Start       string  `json:"start"`
	End         string  `json:"end,omitempty"`
	Color       string  `json:"color"`
	AllDay      bool    `json:"allDay"`
	Description string  `json:"description"`
	Result      *string `json:"result,omitempty"`
	Bold        bool    `json:"bold"`
	Phase       string  `json:"phase,omitempty"`
	Deadline    *string `json:"deadline,omitempty"`
	BoardDay    string  `json:"boardDay,omitempty"`
}

type EmailEventInput struct {
	Title         string `json:"title"`
	Start         string `json:"start"`
	End           string `json:"end,omitempty"`
	AllDay        bool   `json:"allDay"`
	Description   string `json:"description"`
	Color         string `json:"color,omitempty"`
	Bold          bool   `json:"bold,omitempty"`
	SourceEmailID string `json:"sourceEmailId"`
}

type EmailSource struct {
	AccountID string
	MessageID string
	Subject   string
	URL       string
}

type BatchResult struct {
	Created   int      `json:"created"`
	Skipped   int      `json:"skipped"`
	Rejected  int      `json:"rejected"`
	Conflicts int      `json:"conflicts"`
	Errors    []string `json:"errors,omitempty"`
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

func (s *Service) GetEventsByDateRange(ctx context.Context, start, end string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, `+eventFields+`
		FROM events
		WHERE start >= ? AND start < date(?, '+1 day')
		AND NOT EXISTS (SELECT 1 FROM board_consolidations m
			WHERE m.event_id = events.id AND m.from_day <= substr(events.start, 1, 10))
		ORDER BY start ASC
	`, start, end)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	return collectEvents(rows)
}

func collectEvents(rows *sql.Rows) ([]Event, error) {
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	return events, nil
}

func (s *Service) CreateEvent(ctx context.Context, input EventInput) (*Event, error) {
	now := s.now()
	input, err := normalizeCreateInput(input, now)
	if err != nil {
		return nil, err
	}
	return s.writeEvent(ctx, func(tx *sql.Tx) (Event, error) {
		return createEventTx(ctx, tx, input, now)
	})
}

func createEventTx(ctx context.Context, tx *sql.Tx, input EventInput, now time.Time) (Event, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO events (
			title, start, end, color, all_day, description, result, bold, phase, deadline,
			created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, COALESCE(?, ''), ?, ?, COALESCE(?, ''), ?, ?)
	`, input.Title, input.Start, input.End, input.Color, boolToInt(input.AllDay), input.Description,
		trimResult(input.Result), boolToInt(input.Bold), input.Phase, input.Deadline, now, now)
	if err != nil {
		return Event{}, fmt.Errorf("create event: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Event{}, fmt.Errorf("read created event id: %w", err)
	}
	event, err := eventByID(ctx, tx, id)
	if err != nil {
		return Event{}, err
	}
	if err := enrollEvent(ctx, tx, event, now); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (s *Service) UpdateEvent(ctx context.Context, id int64, input EventInput) (*Event, error) {
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		return nil, errors.New("title is required")
	}
	phase := strings.TrimSpace(input.Phase)
	if phase != "" {
		validated, err := validatePhase(phase)
		if err != nil {
			return nil, err
		}
		phase = validated
	}
	deadline, err := normalizeDeadline(input.Deadline)
	if err != nil {
		return nil, err
	}
	return s.changeEvent(ctx, id, input.BoardDay, func(event *Event) {
		event.Title, event.Start, event.End = input.Title, input.Start, input.End
		event.Color, event.AllDay, event.Bold = input.Color, input.AllDay, input.Bold
		event.Description = input.Description
		if input.Result != nil {
			event.Result = strings.TrimSpace(*input.Result)
		}
		if deadline != nil {
			event.Deadline = *deadline
		}
		if phase != "" {
			event.Phase = phase
		}
	})
}

func (s *Service) SetEventPhase(ctx context.Context, id int64, phase string) (*Event, error) {
	return s.setPhase(ctx, id, phase, "")
}

func (s *Service) SetBoardEventPhase(ctx context.Context, id int64, phase, day string) (*Event, error) {
	if day == "" {
		return nil, errors.New("board day is required")
	}
	return s.setPhase(ctx, id, phase, day)
}

func (s *Service) setPhase(ctx context.Context, id int64, phase, day string) (*Event, error) {
	phase, err := validatePhase(phase)
	if err != nil {
		return nil, err
	}
	return s.changeEvent(ctx, id, day, func(event *Event) { event.Phase = phase })
}

func (s *Service) DeleteEvent(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	return nil
}

func (s *Service) BatchCreateFromEmail(
	ctx context.Context,
	inputs []EmailEventInput,
	sources map[string]EmailSource,
) (BatchResult, error) {
	result := BatchResult{}
	if len(inputs) == 0 {
		return result, nil
	}
	if len(inputs) > maxBatchSize {
		return result, fmt.Errorf("event batch exceeds %d items", maxBatchSize)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin event batch: %w", err)
	}
	defer tx.Rollback()

	for index, input := range inputs {
		source, ok := sources[input.SourceEmailID]
		if !ok || source.URL == "" {
			result.reject(index, "sourceEmailId is not available in this AI Sync run")
			continue
		}

		event, err := normalizeEmailEvent(input, source.URL)
		if err != nil {
			result.reject(index, err.Error())
			continue
		}
		duplicate, err := hasEmailDuplicate(ctx, tx, source, event)
		if err != nil {
			return result, err
		}
		if duplicate {
			result.Skipped++
			continue
		}
		if conflict, err := hasConflict(ctx, tx, event); err != nil {
			return result, err
		} else if conflict {
			result.Conflicts++
		}

		now := s.now()
		insert, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO events (
				title, start, end, color, all_day, description, bold, phase, deadline,
				source_type, source_account_id, source_message_id, source_url,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'todo', '', 'email', ?, ?, ?, ?, ?)
		`, event.Title, event.Start, event.End, event.Color, boolToInt(event.AllDay), event.Description,
			boolToInt(event.Bold), source.AccountID, source.MessageID, source.URL, now, now)
		if err != nil {
			return result, fmt.Errorf("insert email event %d: %w", index+1, err)
		}
		affected, err := insert.RowsAffected()
		if err != nil {
			return result, fmt.Errorf("inspect email event %d: %w", index+1, err)
		}
		if affected == 0 {
			result.Skipped++
		} else {
			id, err := insert.LastInsertId()
			if err != nil {
				return result, err
			}
			created, err := eventByID(ctx, tx, id)
			if err != nil {
				return result, err
			}
			if err := enrollEvent(ctx, tx, created, now); err != nil {
				return result, err
			}
			result.Created++
		}
	}

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit event batch: %w", err)
	}
	return result, nil
}

type eventReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func eventByID(ctx context.Context, db eventReader, id int64) (Event, error) {
	return scanEvent(db.QueryRowContext(ctx, "SELECT id, "+eventFields+" FROM events WHERE id = ?", id))
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(row rowScanner, extra ...any) (Event, error) {
	var event Event
	var allDay, bold int
	var end, description, result, deadline, registrations sql.NullString
	var createdAt, updatedAt sql.NullTime
	fields := []any{
		&event.ID, &event.Title, &event.Start, &end, &event.Color,
		&allDay, &description, &result, &bold, &event.Phase, &deadline,
		&registrations, &createdAt, &updatedAt,
	}
	if err := row.Scan(append(fields, extra...)...); err != nil {
		return Event{}, fmt.Errorf("scan event: %w", err)
	}
	event.AllDay = allDay == 1
	event.Bold = bold == 1
	event.CreatedAt, event.UpdatedAt = createdAt.Time, updatedAt.Time
	if end.Valid {
		event.End = end.String
	}
	if description.Valid {
		event.Description = description.String
	}
	if result.Valid {
		event.Result = result.String
	}
	if deadline.Valid {
		event.Deadline = deadline.String
	}
	if registrations.Valid {
		decoded, err := decodeRegistrations(registrations.String)
		if err != nil {
			return Event{}, err
		}
		event.Registrations = decoded
	}
	return event, nil
}

func normalizeCreateInput(input EventInput, now time.Time) (EventInput, error) {
	if input.BoardDay != "" {
		if _, err := normalizeBoardDay(input.BoardDay); err != nil {
			return EventInput{}, err
		}
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		return EventInput{}, errors.New("title is required")
	}

	phase, err := normalizePhase(input.Phase)
	if err != nil {
		return EventInput{}, err
	}
	input.Phase = phase

	deadline, err := normalizeDeadline(input.Deadline)
	if err != nil {
		return EventInput{}, err
	}
	input.Deadline = deadline

	if strings.TrimSpace(input.Start) == "" {
		input.Start = now.Format(dateLayout) + "T00:00:00"
		if strings.TrimSpace(input.End) == "" {
			input.End = input.Start
		}
		input.AllDay = true
	}
	if !colorPattern.MatchString(input.Color) {
		input.Color = defaultEventColor
	}
	return input, nil
}

func trimResult(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func normalizePhase(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return PhaseTodo, nil
	}
	return validatePhase(value)
}

func validatePhase(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case PhaseTodo, PhaseProgressing, PhaseDone:
		return value, nil
	default:
		return "", errors.New("phase must be todo, progressing, or done")
	}
}

func normalizeDeadline(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	deadline := strings.TrimSpace(*value)
	if deadline == "" {
		return &deadline, nil
	}
	parsed, err := time.Parse(dateLayout, deadline)
	if err != nil || parsed.Format(dateLayout) != deadline {
		return nil, errors.New("deadline must use YYYY-MM-DD")
	}
	return &deadline, nil
}

func normalizeEmailEvent(input EmailEventInput, sourceURL string) (EventInput, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return EventInput{}, errors.New("title is required")
	}
	if len([]rune(title)) > 200 {
		return EventInput{}, errors.New("title exceeds 200 characters")
	}

	start, err := normalizeDateTime(input.Start, input.AllDay)
	if err != nil {
		return EventInput{}, fmt.Errorf("invalid start: %w", err)
	}
	end := start
	if strings.TrimSpace(input.End) != "" {
		end, err = normalizeDateTime(input.End, input.AllDay)
		if err != nil {
			return EventInput{}, fmt.Errorf("invalid end: %w", err)
		}
	}
	if end < start {
		return EventInput{}, errors.New("end must not be earlier than start")
	}

	color := input.Color
	if !colorPattern.MatchString(color) {
		color = defaultEventColor
	}
	description := strings.TrimSpace(input.Description)
	if description != "" {
		description += "\n\n"
	}
	description += "Source email: " + sourceURL

	return EventInput{
		Title:       title,
		Start:       start,
		End:         end,
		Color:       color,
		AllDay:      input.AllDay,
		Description: description,
		Bold:        input.Bold,
		Phase:       PhaseTodo,
	}, nil
}

func normalizeDateTime(value string, allDay bool) (string, error) {
	value = strings.TrimSpace(value)
	formats := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", dateLayout}
	for _, layout := range formats {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		if allDay {
			return parsed.Format("2006-01-02") + "T00:00:00", nil
		}
		return parsed.Format("2006-01-02T15:04:05"), nil
	}
	return "", errors.New("use YYYY-MM-DD or an ISO 8601 date-time")
}

func hasConflict(ctx context.Context, tx *sql.Tx, event EventInput) (bool, error) {
	if event.AllDay {
		return false, nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE all_day = 0
		  AND start < ?
		  AND COALESCE(NULLIF(end, ''), start) > ?
	`, event.End, event.Start).Scan(&count); err != nil {
		return false, fmt.Errorf("check event conflict: %w", err)
	}
	return count > 0, nil
}

func hasEmailDuplicate(ctx context.Context, tx *sql.Tx, source EmailSource, event EventInput) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE source_type = 'email'
		  AND source_account_id = ?
		  AND source_message_id = ?
		  AND title = ?
		  AND start = ?
	`, source.AccountID, source.MessageID, event.Title, event.Start).Scan(&count); err != nil {
		return false, fmt.Errorf("check duplicate email event: %w", err)
	}
	return count > 0, nil
}

func (r *BatchResult) reject(index int, reason string) {
	r.Rejected++
	if len(r.Errors) < 10 {
		r.Errors = append(r.Errors, fmt.Sprintf("item %d: %s", index+1, reason))
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
