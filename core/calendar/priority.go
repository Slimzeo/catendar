package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// Manual order is stored apart from daily revisions. A snapshot describes the
// whole visible column for one day and phase, and is replaced only when a drop
// actually changes that column. Reads take the newest snapshot on or before the
// day being viewed, so carried open cards keep their order while earlier days
// and later explicit snapshots stay untouched.
const rankedBoardFrom = `
 FROM (` + boardSelect + `) v
 LEFT JOIN board_orders o ON o.event_id = v.id AND o.phase = v.phase
  AND o.day = (SELECT MAX(snapshot.day) FROM board_orders snapshot WHERE snapshot.phase = v.phase AND snapshot.day <= ?1)
`
const byBoardPriority = " ORDER BY o.position IS NULL, o.position, v.start, v.id"

func orderedColumnIDs(ctx context.Context, tx *sql.Tx, day, today, phase string) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT v.id"+rankedBoardFrom+" WHERE v.phase = ?3"+byBoardPriority, day, today, phase)
	if err != nil {
		return nil, fmt.Errorf("read column order: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ReorderBoardEvent moves a visible card before another card in the same phase
// column, or to the end when beforeID is zero. Both identities are resolved
// against the requested day's visible column, so a stale, deleted, or
// different-phase card cannot change the saved order. Only the order snapshot
// is written; dates, phases, ownership, and daily history stay untouched.
func (s *Service) ReorderBoardEvent(ctx context.Context, day, phase string, id, beforeID int64) error {
	day, err := normalizeBoardDay(day)
	if err != nil {
		return err
	}
	phase, err = validatePhase(phase)
	if err != nil {
		return err
	}
	if id <= 0 {
		return errors.New("board event id must be positive")
	}
	if beforeID < 0 {
		return errors.New("board anchor id must not be negative")
	}

	today := s.now().Format(dateLayout)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin board reorder: %w", err)
	}
	defer tx.Rollback()

	column, err := orderedColumnIDs(ctx, tx, day, today, phase)
	if err != nil {
		return err
	}
	from := slices.Index(column, id)
	if from < 0 {
		return boardOrderMiss(id, day, phase)
	}
	at := len(column)
	if beforeID != 0 {
		at = slices.Index(column, beforeID)
		if at < 0 {
			return boardOrderMiss(beforeID, day, phase)
		}
	}
	if from < at {
		at--
	}
	if from == at {
		return nil
	}
	column = slices.Delete(column, from, from+1)
	column = slices.Insert(column, at, id)
	if err := writeBoardOrder(ctx, tx, day, phase, column); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit board reorder: %w", err)
	}
	return nil
}

func boardOrderMiss(id int64, day, phase string) error {
	return fmt.Errorf("board event %d is not visible on %s in phase %s: %w", id, day, phase, sql.ErrNoRows)
}

func writeBoardOrder(ctx context.Context, tx *sql.Tx, day, phase string, order []int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM board_orders WHERE day = ? AND phase = ?", day, phase); err != nil {
		return fmt.Errorf("clear board order: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO board_orders (day, phase, event_id, position) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for position, id := range order {
		if _, err := stmt.ExecContext(ctx, day, phase, id, position); err != nil {
			return fmt.Errorf("save board order: %w", err)
		}
	}
	return nil
}
