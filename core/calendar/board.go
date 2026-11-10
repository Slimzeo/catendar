package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// One revision per task and local day. An open revision continues until the
// next revision. A completion or an explicit reschedule can end it earlier.
const boardSchema = `
	CREATE TABLE IF NOT EXISTS event_days (
		event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
		day TEXT NOT NULL, through_day TEXT,
		title TEXT NOT NULL, start TEXT NOT NULL, end TEXT, color TEXT NOT NULL,
		all_day INTEGER NOT NULL, description TEXT NOT NULL, result TEXT NOT NULL,
		bold INTEGER NOT NULL, phase TEXT NOT NULL, deadline TEXT NOT NULL,
		registrations TEXT NOT NULL DEFAULT '[]',
		created_at DATETIME, updated_at DATETIME,
		PRIMARY KEY (event_id, day)
	);
	CREATE TABLE IF NOT EXISTS board_consolidations (
		event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
		summary_id INTEGER REFERENCES events(id) ON DELETE SET NULL,
		from_day TEXT NOT NULL,
		CHECK (event_id <> summary_id)
	);
	CREATE TABLE IF NOT EXISTS board_meta (
		id INTEGER PRIMARY KEY CHECK (id = 1), seeded_at DATETIME NOT NULL
	);
	CREATE TABLE IF NOT EXISTS board_orders (
		day TEXT NOT NULL, phase TEXT NOT NULL,
		event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
		position INTEGER NOT NULL,
		PRIMARY KEY (phase, day, event_id),
		UNIQUE (phase, day, position)
	);
`

const eventFields = `title, start, end, color, all_day, description, result,
	bold, phase, deadline, registrations, created_at, updated_at`

// ensureRegistrationColumns upgrades databases created before action details
// existed. It runs before seeding so newly written board rows already carry the
// column.
func ensureRegistrationColumns(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"events", "event_days", "chat_activities"} {
		var count int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM pragma_table_info('"+table+"') WHERE name = 'registrations'",
		).Scan(&count); err != nil {
			return fmt.Errorf("inspect %s.registrations: %w", table, err)
		}
		if count > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"ALTER TABLE "+table+" ADD COLUMN registrations TEXT NOT NULL DEFAULT '[]'",
		); err != nil {
			return fmt.Errorf("add %s.registrations: %w", table, err)
		}
	}
	return nil
}

// Scheduled dates use the same wall date as the month calendar. Audit
// timestamps, in contrast, are instants and must be converted to local time.
func scheduleDay(start string) string {
	if len(start) < len(dateLayout) {
		return ""
	}
	day := start[:len(dateLayout)]
	if _, err := time.Parse(dateLayout, day); err != nil {
		return ""
	}
	return day
}

func normalizeBoardDay(day string) (string, error) {
	day = strings.TrimSpace(day)
	if _, err := time.Parse(dateLayout, day); err != nil {
		return "", errors.New("board day must use YYYY-MM-DD")
	}
	return day, nil
}

// SeedBoard enrolls only yesterday's, today's and future scheduled cards, and
// recently worked-on open cards. Older records remain on their calendar date.
// Creating the marker and the initial revisions is a single transaction.
func SeedBoard(ctx context.Context, db *sql.DB, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, boardSchema); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, chatSchema); err != nil {
		return fmt.Errorf("create chat schema: %w", err)
	}
	if err := ensureRegistrationColumns(ctx, tx); err != nil {
		return err
	}
	// Existing cards and historical snapshots gain their editable details even
	// when the board itself was seeded by an older version.
	if err := upgradeRegistrationDescriptions(ctx, tx, now.Format(dateLayout)); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO board_meta (id, seeded_at) VALUES (1, ?)", now)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tx.Commit()
	}
	yesterday, today := now.AddDate(0, 0, -1).Format(dateLayout), now.Format(dateLayout)
	rows, err := tx.QueryContext(ctx, "SELECT id, "+eventFields+" FROM events")
	if err != nil {
		return err
	}
	defer rows.Close()
	type seed struct {
		day   string
		event Event
	}
	var seeds []seed
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return err
		}
		day := scheduleDay(event.Start)
		if day == "" {
			continue
		}
		if day < yesterday {
			worked := event.UpdatedAt.In(now.Location()).Format(dateLayout)
			if event.Phase == PhaseDone || worked < yesterday || worked > today {
				continue
			}
			day = worked
		}
		seeds = append(seeds, seed{day, event})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, s := range seeds {
		if err := writeDay(ctx, tx, s.event, s.day, true); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func writeDay(ctx context.Context, tx *sql.Tx, event Event, day string, carry bool) error {
	var through any
	if !carry || event.Phase == PhaseDone {
		through = day
	}
	registrations, err := encodeRegistrations(event.Registrations)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO event_days (event_id, day, through_day, `+eventFields+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, day, through,
		event.Title, event.Start, event.End, event.Color, event.AllDay, event.Description, event.Result,
		event.Bold, event.Phase, event.Deadline, registrations, event.CreatedAt, event.UpdatedAt)
	return err
}

// Use the last actual revision as of the requested day. Looking into the
// future shows only explicit appointments. Neither path writes on read.
// A folded card keeps its identity and all revisions before the effective
// day. Deleting its summary must not bring completed implementation steps back.
const boardSelect = `
	SELECT * FROM (
	SELECT d.event_id AS id, ` + eventFields + ` FROM event_days d
	WHERE d.day = (SELECT MAX(h.day) FROM event_days h WHERE h.event_id = d.event_id AND h.day <= ?1)
		AND (d.through_day IS NULL OR d.through_day >= ?1)
		AND (d.day = ?1 OR ?1 <= ?2)
	UNION ALL
	SELECT id, ` + eventFields + ` FROM events e
	WHERE e.start >= ?1 AND e.start < date(?1, '+1 day')
		AND NOT EXISTS (SELECT 1 FROM event_days h WHERE h.event_id = e.id AND h.day <= ?1)
	) AS visible
	WHERE NOT EXISTS (SELECT 1 FROM board_consolidations m
		WHERE m.event_id = visible.id AND m.from_day <= ?1)
`

func (s *Service) GetBoardEvents(ctx context.Context, day string) ([]Event, error) {
	day, err := normalizeBoardDay(day)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT v.*"+rankedBoardFrom+byBoardPriority, day, s.now().Format(dateLayout))
	if err != nil {
		return nil, fmt.Errorf("read daily board: %w", err)
	}
	return collectEvents(rows)
}

func boardEvent(ctx context.Context, tx *sql.Tx, id int64, day, today string) (Event, error) {
	return scanEvent(tx.QueryRowContext(ctx, "SELECT * FROM ("+boardSelect+") WHERE id = ?3", day, today, id))
}

func hasLaterRevision(ctx context.Context, tx *sql.Tx, id int64, day string) (bool, error) {
	var later bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM event_days WHERE event_id = ? AND day > ?)", id, day).Scan(&later)
	return later, err
}

// Freeze a legacy task's original record before editing its canonical row.
func preserveOriginal(ctx context.Context, tx *sql.Tx, event Event, day string) error {
	origin := scheduleDay(event.Start)
	if origin == "" || origin >= day {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM event_days WHERE event_id = ? AND day = ?)", event.ID, origin).Scan(&exists); err != nil || exists {
		return err
	}
	return writeDay(ctx, tx, event, origin, false)
}

func enrollEvent(ctx context.Context, tx *sql.Tx, event Event, now time.Time) error {
	day := scheduleDay(event.Start)
	if day == "" {
		return nil
	}
	// A task first created against an old date has no intervening work history.
	if day < now.AddDate(0, 0, -1).Format(dateLayout) && event.Phase != PhaseDone {
		if err := writeDay(ctx, tx, event, day, false); err != nil {
			return err
		}
		day = now.Format(dateLayout)
	}
	return writeDay(ctx, tx, event, day, true)
}

func reschedule(ctx context.Context, tx *sql.Tx, old, updated Event, now time.Time) error {
	today := now.Format(dateLayout)
	if err := preserveOriginal(ctx, tx, old, today); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM event_days WHERE event_id = ? AND day >= ?", old.ID, today); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE event_days SET through_day = ?
		WHERE event_id = ? AND through_day IS NULL AND day =
		(SELECT MAX(day) FROM event_days WHERE event_id = ? AND day < ?)`,
		now.AddDate(0, 0, -1).Format(dateLayout), old.ID, old.ID, today); err != nil {
		return err
	}
	day := scheduleDay(updated.Start)
	if day == "" {
		return nil
	}
	if day < today {
		if err := preserveOriginal(ctx, tx, updated, today); err != nil {
			return err
		}
		day = today
	}
	return writeDay(ctx, tx, updated, day, true)
}

func (s *Service) writeEvent(ctx context.Context, fn func(*sql.Tx) (Event, error)) (*Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	event, err := fn(tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &event, nil
}

// Board edits start with exactly what the user saw on that day. Calendar/CLI
// edits start with the canonical event. Both commit the revision atomically.
func (s *Service) changeEvent(ctx context.Context, id int64, boardDay string, apply func(*Event)) (*Event, error) {
	now := s.now()
	return s.writeEvent(ctx, func(tx *sql.Tx) (Event, error) {
		var before Event
		updated, err := changeEventTx(ctx, tx, id, boardDay, now, func(event *Event) {
			before = *event
			apply(event)
		})
		if err == nil && (boardDay == "" || boardDay >= now.Format(dateLayout)) {
			err = recordManualChatEdit(ctx, tx, before, updated, now)
		}
		return updated, err
	})
}

func changeEventTx(ctx context.Context, tx *sql.Tx, id int64, boardDay string, now time.Time, apply func(*Event)) (Event, error) {
	today := now.Format(dateLayout)
	if boardDay != "" {
		var err error
		boardDay, err = normalizeBoardDay(boardDay)
		if err != nil {
			return Event{}, err
		}
	}
	current, err := eventByID(ctx, tx, id)
	if err != nil {
		return Event{}, err
	}
	day, before := max(today, scheduleDay(current.Start)), current
	if boardDay != "" {
		day = boardDay
		before, err = boardEvent(ctx, tx, id, day, today)
		if err != nil {
			return Event{}, err
		}
	}
	updated := before
	apply(&updated)
	updated.UpdatedAt = now

	if day < today {
		later, err := hasLaterRevision(ctx, tx, id, day)
		if err != nil {
			return Event{}, err
		}
		if later {
			// A correction to history cannot replace a later decision, or
			// reopen a gap that was explicitly closed by rescheduling.
			var through sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT through_day FROM event_days
				WHERE event_id = ? AND day <= ? ORDER BY day DESC LIMIT 1`, id, day).Scan(&through)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return Event{}, err
			}
			if err := writeDay(ctx, tx, updated, day, true); err != nil {
				return Event{}, err
			}
			if through.Valid && before.Phase != PhaseDone && updated.Phase != PhaseDone {
				_, err = tx.ExecContext(ctx, "UPDATE event_days SET through_day = ? WHERE event_id = ? AND day = ?", through.String, id, day)
				return updated, err
			}
			return updated, nil
		}
	}

	if scheduleDay(before.Start) != scheduleDay(updated.Start) {
		err = reschedule(ctx, tx, current, updated, now)
	} else {
		err = preserveOriginal(ctx, tx, current, day)
		if err == nil {
			// Editing an already archived completion in the month calendar
			// should not introduce a new completed card on today's board.
			visible := true
			if boardDay == "" && before.Phase == PhaseDone && updated.Phase == PhaseDone {
				_, err = boardEvent(ctx, tx, id, day, today)
				if errors.Is(err, sql.ErrNoRows) {
					visible, err = false, nil
				}
			}
			if err == nil && visible {
				err = writeDay(ctx, tx, updated, day, true)
			}
		}
	}
	if err != nil {
		return Event{}, err
	}
	registrations, err := encodeRegistrations(updated.Registrations)
	if err != nil {
		return Event{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE events SET title=?, start=?, end=?, color=?, all_day=?,
		description=?, result=?, bold=?, phase=?, deadline=?, registrations=?, updated_at=? WHERE id=?`,
		updated.Title, updated.Start, updated.End, updated.Color, updated.AllDay, updated.Description,
		updated.Result, updated.Bold, updated.Phase, updated.Deadline, registrations, now, id)
	return updated, err
}
