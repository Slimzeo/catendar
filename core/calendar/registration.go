package calendar

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	maxRegistrations    = 32
	maxRegistrationName = 200
	maxRegistrationURL  = 2048
	maxRegistrationText = 500
)

// Registration is one verified action link for a task. Name identifies the
// action across revisions so later evidence can fill missing parts without
// replacing an existing link or cutoff.
type Registration struct {
	Name         string `json:"name"`
	URL          string `json:"url,omitempty"`
	Deadline     string `json:"deadline,omitempty"`
	DeadlineText string `json:"deadlineText,omitempty"`
}

func normalizeRegistrations(values []Registration) ([]Registration, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > maxRegistrations {
		return nil, fmt.Errorf("registrations exceed %d items", maxRegistrations)
	}
	normalized := make([]Registration, 0, len(values))
	for index, value := range values {
		registration, err := normalizeRegistration(value)
		if err != nil {
			return nil, fmt.Errorf("registration %d: %w", index+1, err)
		}
		normalized = append(normalized, registration)
	}
	return normalized, nil
}

func normalizeRegistration(value Registration) (Registration, error) {
	value.Name = strings.TrimSpace(value.Name)
	value.URL = strings.TrimSpace(value.URL)
	value.Deadline = strings.TrimSpace(value.Deadline)
	value.DeadlineText = strings.TrimSpace(value.DeadlineText)

	if value.Name == "" {
		return Registration{}, errors.New("registration name is required")
	}
	if len([]rune(value.Name)) > maxRegistrationName {
		return Registration{}, fmt.Errorf("registration name exceeds %d characters", maxRegistrationName)
	}
	if containsControl(value.Name) {
		return Registration{}, errors.New("registration name contains control characters")
	}
	if value.URL == "" && value.Deadline == "" && value.DeadlineText == "" {
		return Registration{}, fmt.Errorf("registration %q needs a URL or deadline", value.Name)
	}
	if value.URL != "" {
		if err := validateRegistrationURL(value.URL); err != nil {
			return Registration{}, fmt.Errorf("registration %q: %w", value.Name, err)
		}
	}
	if value.Deadline != "" {
		if len(value.Deadline) != len(dateLayout) {
			return Registration{}, fmt.Errorf("registration %q deadline must use YYYY-MM-DD", value.Name)
		}
		parsed, err := time.Parse(dateLayout, value.Deadline)
		if err != nil || parsed.Format(dateLayout) != value.Deadline {
			return Registration{}, fmt.Errorf("registration %q deadline must use YYYY-MM-DD", value.Name)
		}
	}
	if len([]rune(value.DeadlineText)) > maxRegistrationText {
		return Registration{}, fmt.Errorf("registration %q deadline text exceeds %d characters", value.Name, maxRegistrationText)
	}
	return value, nil
}

func validateRegistrationURL(value string) error {
	if len(value) > maxRegistrationURL {
		return fmt.Errorf("URL exceeds %d characters", maxRegistrationURL)
	}
	if containsControl(value) {
		return errors.New("URL contains control characters")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return errors.New("URL is not valid")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("URL must use http or https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return errors.New("URL must include a host")
	}
	if parsed.User != nil {
		return errors.New("URL must not include credentials")
	}
	return nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func encodeRegistrations(values []Registration) (string, error) {
	if len(values) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode registrations: %w", err)
	}
	return string(encoded), nil
}

func decodeRegistrations(raw string) ([]Registration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var values []Registration
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("decode registrations: %w", err)
	}
	return values, nil
}

func registrationIndex(registrations []Registration, candidate Registration) int {
	key := func(name string) string { return strings.Join(strings.Fields(strings.ToLower(name)), "") }
	name := key(candidate.Name)
	for i, r := range registrations {
		other := key(r.Name)
		if other == name || (candidate.URL != "" && candidate.URL == r.URL && (strings.Contains(name, other) || strings.Contains(other, name))) {
			return i
		}
	}
	return -1
}

var cutoffClock = regexp.MustCompile(`(?i)(?:[01]?[0-9]|2[0-3])[:：][0-5][0-9](?:\s*(?:AM|PM)\b)?`)
var cutoffZone = regexp.MustCompile(`(?i)(?:UTC|GMT)(?:\s*[+-]\s*[0-9]{1,2}(?::?[0-9]{2})?)?|北京时间|北京時間|新加坡时间|纽约时间|太平洋时间|美东时间|中欧时间|\b(?:CST|PST|PDT|EST|EDT|CET|CEST|SGT|JST)\b`)

// The same cutoff is often phrased differently in a follow-up. Keep the
// more precise wording; ask before changing an actual time or timezone.
func compareCutoffText(a, b Registration) (conflict, richer bool) {
	if a.DeadlineText == b.DeadlineText || b.DeadlineText == "" {
		return false, false
	}
	if a.DeadlineText == "" {
		return false, true
	}
	if a.Deadline == "" || a.Deadline != b.Deadline {
		return true, false
	}
	pattern := func(re *regexp.Regexp, text string) string {
		return strings.ToLower(strings.Join(re.FindAllString(strings.ReplaceAll(text, "：", ":"), -1), "/"))
	}
	ac, bc := pattern(cutoffClock, a.DeadlineText), pattern(cutoffClock, b.DeadlineText)
	az, bz := pattern(cutoffZone, a.DeadlineText), pattern(cutoffZone, b.DeadlineText)
	if ac != "" && bc != "" && ac != bc || az != "" && bz != "" && az != bz {
		return true, false
	}
	if ac != "" && bc == "" || az != "" && bz == "" {
		return false, false
	}
	return false, ac == "" && bc != "" || az == "" && bz != ""
}

// mergeRegistrations only grows a card's action details. Missing URL or
// deadline parts are filled in place, new names are appended, and unrelated
// registrations keep their order. overwrite is used only after the user
// accepts a pending review of an incompatible value.
func mergeRegistrations(existing, proposed []Registration, overwrite bool) ([]Registration, bool) {
	merged := append([]Registration(nil), existing...)
	changed := false
	for _, candidate := range proposed {
		index := registrationIndex(merged, candidate)
		if index < 0 {
			if len(merged) >= maxRegistrations {
				continue
			}
			merged = append(merged, candidate)
			changed = true
			continue
		}
		current := merged[index]
		_, richerText := compareCutoffText(current, candidate)
		if overwrite && candidate.Deadline != "" && candidate.Deadline != current.Deadline && candidate.DeadlineText == "" {
			current.DeadlineText = ""
		}
		if candidate.URL != "" && candidate.URL != current.URL && (current.URL == "" || overwrite) {
			current.URL = candidate.URL
			changed = true
		}
		if candidate.Deadline != "" && candidate.Deadline != current.Deadline && (current.Deadline == "" || overwrite) {
			current.Deadline = candidate.Deadline
			changed = true
		}
		if candidate.DeadlineText != "" && candidate.DeadlineText != current.DeadlineText && (richerText || overwrite) {
			current.DeadlineText = candidate.DeadlineText
			changed = true
		}
		merged[index] = current
	}
	return merged, changed
}

func registrationsWouldChange(existing, proposed []Registration) bool {
	_, changed := mergeRegistrations(existing, proposed, false)
	return changed
}

// registrationConflictReason reports an incompatible existing URL or cutoff.
// Filling a missing part is additive and never needs review.
func registrationConflictReason(existing, proposed []Registration) string {
	for _, candidate := range proposed {
		index := registrationIndex(existing, candidate)
		if index < 0 {
			continue
		}
		current := existing[index]
		if candidate.URL != "" && current.URL != "" && candidate.URL != current.URL {
			return fmt.Sprintf("the suggestion would change the existing registration %q URL", current.Name)
		}
		if candidate.Deadline != "" && current.Deadline != "" && candidate.Deadline != current.Deadline {
			return fmt.Sprintf("the suggestion would change the existing registration %q deadline", current.Name)
		}
		if conflict, _ := compareCutoffText(current, candidate); conflict {
			return fmt.Sprintf("the suggestion would change the existing registration %q deadline", current.Name)
		}
	}
	return ""
}

// deriveRegistrationDeadline chooses the nearest cutoff that is still
// applicable. A future cutoff always wins over a past one; if every cutoff has
// passed, the latest past date is the least stale value.
func deriveRegistrationDeadline(registrations []Registration, today string) string {
	var future, past string
	for _, registration := range registrations {
		if registration.Deadline == "" {
			continue
		}
		if registration.Deadline >= today {
			if future == "" || registration.Deadline < future {
				future = registration.Deadline
			}
			continue
		}
		if past == "" || registration.Deadline > past {
			past = registration.Deadline
		}
	}
	if future != "" {
		return future
	}
	return past
}

// writeRegistrationDetailsTx persists action details after a chat task is
// created, before the caller records its activity.
func writeRegistrationDetailsTx(ctx context.Context, tx *sql.Tx, event *Event, now time.Time) error {
	raw, err := encodeRegistrations(event.Registrations)
	if err != nil {
		return err
	}
	event.UpdatedAt = now
	if _, err := tx.ExecContext(ctx,
		"UPDATE events SET registrations = ?, deadline = ?, updated_at = ? WHERE id = ?",
		raw, event.Deadline, now, event.ID,
	); err != nil {
		return fmt.Errorf("update event registrations: %w", err)
	}
	day := scheduleDay(event.Start)
	if day == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE event_days SET registrations = ?, deadline = ?, updated_at = ? WHERE event_id = ? AND day = ?",
		raw, event.Deadline, now, event.ID, day,
	); err != nil {
		return fmt.Errorf("update board registrations: %w", err)
	}
	return nil
}

// NULL records an explicit human choice, including clearing a date. A future
// sync may move only a deadline that still matches its own previous value.
func recordRegistrationDeadline(ctx context.Context, tx *sql.Tx, id int64, value any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO chat_registration_deadlines(event_id,derived) VALUES(?,?) ON CONFLICT(event_id) DO UPDATE SET derived=excluded.derived`, id, value)
	return err
}
