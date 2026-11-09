package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	routineWorkspaceName = "Daily Routine"
	maxWorkspaceNameLen  = 100
	maxWorkspaceHintLen  = 500
)

// Named project workspaces are real rows. Workspace zero is only the "All"
// aggregate view in the UI and is never stored on a task.
const workspaceSchema = `
	CREATE TABLE IF NOT EXISTS workspaces (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		hint TEXT NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_workspaces_name ON workspaces(name COLLATE NOCASE);
`

var seededWorkspaces = []struct {
	id   int64
	name string
}{
	{RoutineWorkspaceID, routineWorkspaceName},
	{2, "Seekino"},
	{3, "个人 IP 打造"},
}

// ensureWorkspacesTx installs the workspace schema and guarantees the three
// built-in projects without overwriting later renames or hints.
func ensureWorkspacesTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, workspaceSchema); err != nil {
		return fmt.Errorf("create workspace schema: %w", err)
	}
	for _, workspace := range seededWorkspaces {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO workspaces (id, name, hint) VALUES (?, ?, '')
		`, workspace.id, workspace.name); err != nil {
			return fmt.Errorf("seed workspace %d: %w", workspace.id, err)
		}
	}
	return nil
}

func (s *Service) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, hint FROM workspaces ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()
	workspaces := make([]Workspace, 0)
	for rows.Next() {
		var workspace Workspace
		if err := rows.Scan(&workspace.ID, &workspace.Name, &workspace.Hint); err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		workspaces = append(workspaces, workspace)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read workspaces: %w", err)
	}
	return workspaces, nil
}

func (s *Service) CreateWorkspace(ctx context.Context, name, hint string) (*Workspace, error) {
	name, hint, err := normalizeWorkspaceFields(name, hint)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin create workspace: %w", err)
	}
	defer tx.Rollback()
	if err := ensureWorkspaceNameAvailable(ctx, tx, name, 0); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO workspaces (name, hint) VALUES (?, ?)", name, hint)
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("read created workspace id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit create workspace: %w", err)
	}
	return &Workspace{ID: id, Name: name, Hint: hint}, nil
}

func (s *Service) UpdateWorkspace(ctx context.Context, id int64, name, hint string) (*Workspace, error) {
	if id <= 0 {
		return nil, errors.New("workspace id is required")
	}
	name, hint, err := normalizeWorkspaceFields(name, hint)
	if err != nil {
		return nil, err
	}
	if id == RoutineWorkspaceID && name != routineWorkspaceName {
		return nil, errors.New("the Daily Routine workspace cannot be renamed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin update workspace: %w", err)
	}
	defer tx.Rollback()
	var currentName string
	if err := tx.QueryRowContext(ctx, "SELECT name FROM workspaces WHERE id = ?", id).Scan(&currentName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("workspace %d not found: %w", id, sql.ErrNoRows)
		}
		return nil, fmt.Errorf("read workspace: %w", err)
	}
	if id == RoutineWorkspaceID && currentName != routineWorkspaceName {
		return nil, errors.New("the Daily Routine workspace cannot be renamed")
	}
	if err := ensureWorkspaceNameAvailable(ctx, tx, name, id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE workspaces SET name = ?, hint = ? WHERE id = ?", name, hint, id); err != nil {
		return nil, fmt.Errorf("update workspace: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit update workspace: %w", err)
	}
	return &Workspace{ID: id, Name: name, Hint: hint}, nil
}

func normalizeWorkspaceFields(name, hint string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", errors.New("workspace name is required")
	}
	if len([]rune(name)) > maxWorkspaceNameLen {
		return "", "", fmt.Errorf("workspace name exceeds %d characters", maxWorkspaceNameLen)
	}
	if strings.EqualFold(name, "All") {
		return "", "", errors.New(`workspace name "All" is reserved for the aggregate view`)
	}
	hint = strings.TrimSpace(hint)
	if len([]rune(hint)) > maxWorkspaceHintLen {
		return "", "", fmt.Errorf("workspace hint exceeds %d characters", maxWorkspaceHintLen)
	}
	return name, hint, nil
}

func ensureWorkspaceNameAvailable(ctx context.Context, tx *sql.Tx, name string, exceptID int64) error {
	rows, err := tx.QueryContext(ctx, "SELECT id, name FROM workspaces WHERE id <> ?", exceptID)
	if err != nil {
		return fmt.Errorf("check workspace name: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var existing string
		if err := rows.Scan(&id, &existing); err != nil {
			return fmt.Errorf("scan workspace name: %w", err)
		}
		if strings.EqualFold(existing, name) {
			return fmt.Errorf("workspace %q already exists", name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read workspace names: %w", err)
	}
	return nil
}

func workspaceExists(ctx context.Context, q eventReader, id int64) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	var exists bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = ?)", id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check workspace: %w", err)
	}
	return exists, nil
}

func validateWorkspace(ctx context.Context, q eventReader, id int64) error {
	if id <= 0 {
		return errors.New("workspace is required")
	}
	exists, err := workspaceExists(ctx, q, id)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("workspace %d does not exist", id)
	}
	return nil
}
