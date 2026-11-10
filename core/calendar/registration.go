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
	maxRegistrationName = 200
	maxRegistrationURL  = 2048
	maxRegistrationText = 500
)

// Registration is one verified action link for a task. Name identifies the
// action across revisions so later evidence can fill missing parts without
// replacing an existing link or cutoff. Deadline describes signing up;
// SubmissionDeadline describes the later deliverable and stays independent.
type Registration struct {
	Name                   string `json:"name"`
	URL                    string `json:"url,omitempty"`
	Deadline               string `json:"deadline,omitempty"`
	DeadlineText           string `json:"deadlineText,omitempty"`
	SubmissionDeadline     string `json:"submissionDeadline,omitempty"`
	SubmissionDeadlineText string `json:"submissionDeadlineText,omitempty"`
}

func normalizeRegistrations(values []Registration) ([]Registration, error) {
	if len(values) == 0 {
		return nil, nil
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
	value.SubmissionDeadline = strings.TrimSpace(value.SubmissionDeadline)
	value.SubmissionDeadlineText = strings.TrimSpace(value.SubmissionDeadlineText)

	if value.Name == "" {
		return Registration{}, errors.New("registration name is required")
	}
	if len([]rune(value.Name)) > maxRegistrationName {
		return Registration{}, fmt.Errorf("registration name exceeds %d characters", maxRegistrationName)
	}
	if containsControl(value.Name) {
		return Registration{}, errors.New("registration name contains control characters")
	}
	if value.URL == "" && value.Deadline == "" && value.DeadlineText == "" &&
		value.SubmissionDeadline == "" && value.SubmissionDeadlineText == "" {
		return Registration{}, fmt.Errorf("registration %q needs a URL or deadline", value.Name)
	}
	if value.URL != "" {
		if err := validateRegistrationURL(value.URL); err != nil {
			return Registration{}, fmt.Errorf("registration %q: %w", value.Name, err)
		}
	}
	if value.Deadline != "" {
		if err := validateRegistrationDay(value.Name, "deadline", value.Deadline); err != nil {
			return Registration{}, err
		}
	}
	if value.SubmissionDeadline != "" {
		if err := validateRegistrationDay(value.Name, "submission deadline", value.SubmissionDeadline); err != nil {
			return Registration{}, err
		}
	}
	if len([]rune(value.DeadlineText)) > maxRegistrationText {
		return Registration{}, fmt.Errorf("registration %q deadline text exceeds %d characters", value.Name, maxRegistrationText)
	}
	if len([]rune(value.SubmissionDeadlineText)) > maxRegistrationText {
		return Registration{}, fmt.Errorf("registration %q submission deadline text exceeds %d characters", value.Name, maxRegistrationText)
	}
	return value, nil
}

func validateRegistrationDay(name, field, value string) error {
	if len(value) != len(dateLayout) {
		return fmt.Errorf("registration %q %s must use YYYY-MM-DD", name, field)
	}
	parsed, err := time.Parse(dateLayout, value)
	if err != nil || parsed.Format(dateLayout) != value {
		return fmt.Errorf("registration %q %s must use YYYY-MM-DD", name, field)
	}
	return nil
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
		if other == name {
			return i
		}
		// A directory link is shared by many unrelated activities, so it only
		// identifies the same action together with a real name alias.
		if candidate.URL != "" && candidate.URL == r.URL &&
			!registrationDirectoryURL(r.URL) && registrationNameAlias(r.Name, candidate.Name) {
			return i
		}
	}
	return -1
}

// registrationDirectoryURL reports whether a URL is a listing or site root
// rather than one activity's form. Equal directory URLs must not merge rows.
func registrationDirectoryURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Path == "" || parsed.Path == "/" || strings.HasSuffix(parsed.Path, "/")
}

// registrationNameAlias accepts a whole-word prefix, suffix or inner phrase,
// so "Orbit Workshop" can absorb "Workshop" without treating arbitrary
// substrings as aliases. Names written without spaces alias at their edges.
func registrationNameAlias(a, b string) bool {
	a = strings.ToLower(strings.Join(strings.Fields(a), " "))
	b = strings.ToLower(strings.Join(strings.Fields(b), " "))
	if a == "" || b == "" || a == b {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	if strings.HasPrefix(b, a+" ") || strings.HasSuffix(b, " "+a) || strings.Contains(b, " "+a+" ") {
		return true
	}
	return (registrationContainsHan(a) || registrationContainsHan(b)) && (strings.HasPrefix(b, a) || strings.HasSuffix(b, a))
}

func registrationContainsHan(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
}

var cutoffClock = regexp.MustCompile(`(?i)(?:[01]?[0-9]|2[0-3])[:：][0-5][0-9](?:\s*(?:AM|PM)\b)?`)
var cutoffZone = regexp.MustCompile(`(?i)(?:UTC|GMT)(?:\s*[+-]\s*[0-9]{1,2}(?::?[0-9]{2})?)?|北京时间|北京時間|新加坡时间|纽约时间|太平洋时间|美东时间|中欧时间|\b(?:CST|PST|PDT|EST|EDT|CET|CEST|SGT|JST)\b`)

// The same cutoff is often phrased differently in a follow-up. Keep the
// more precise wording; ask before changing an actual time or timezone.
func compareCutoffText(a, b Registration) (conflict, richer bool) {
	return compareRegistrationDeadlineText(a.Deadline, b.Deadline, a.DeadlineText, b.DeadlineText)
}

func compareRegistrationDeadlineText(aDay, bDay, aText, bText string) (conflict, richer bool) {
	if aText == bText || bText == "" {
		return false, false
	}
	if aText == "" {
		return false, true
	}
	if aDay == "" || aDay != bDay {
		return true, false
	}
	pattern := func(re *regexp.Regexp, text string) string {
		return strings.ToLower(strings.Join(re.FindAllString(strings.ReplaceAll(text, "：", ":"), -1), "/"))
	}
	ac, bc := pattern(cutoffClock, aText), pattern(cutoffClock, bText)
	az, bz := pattern(cutoffZone, aText), pattern(cutoffZone, bText)
	if ac != "" && bc != "" && ac != bc || az != "" && bz != "" && az != bz {
		return true, false
	}
	if ac != "" && bc == "" || az != "" && bz == "" {
		return false, false
	}
	return false, ac == "" && bc != "" || az == "" && bz != ""
}

// mergeRegistrationDeadline fills one deadline kind without touching the
// other. Missing parts are filled in place; overwrite is used only after the
// user accepts a pending review of an incompatible value.
func mergeRegistrationDeadline(day, text, candidateDay, candidateText string, overwrite bool) (string, string, bool) {
	_, richerText := compareRegistrationDeadlineText(day, candidateDay, text, candidateText)
	changed := false
	if overwrite && candidateDay != "" && candidateDay != day && candidateText == "" && text != "" {
		text = ""
		changed = true
	}
	if candidateDay != "" && candidateDay != day && (day == "" || overwrite) {
		day = candidateDay
		changed = true
	}
	if candidateText != "" && candidateText != text && (richerText || overwrite) {
		text = candidateText
		changed = true
	}
	return day, text, changed
}

// mergeRegistrations only grows a card's action details. Missing URL or
// deadline parts are filled in place for both signup and submission, new names
// are appended, and unrelated registrations keep their order. overwrite is used
// only after the user accepts a pending review of an incompatible value.
func mergeRegistrations(existing, proposed []Registration, overwrite bool) ([]Registration, bool) {
	merged := append([]Registration(nil), existing...)
	changed := false
	for _, candidate := range proposed {
		index := registrationIndex(merged, candidate)
		if index < 0 {
			merged = append(merged, candidate)
			changed = true
			continue
		}
		current := merged[index]
		if candidate.URL != "" && candidate.URL != current.URL && (current.URL == "" || overwrite) {
			current.URL = candidate.URL
			changed = true
		}
		var deadlineChanged, submissionChanged bool
		current.Deadline, current.DeadlineText, deadlineChanged = mergeRegistrationDeadline(
			current.Deadline, current.DeadlineText, candidate.Deadline, candidate.DeadlineText, overwrite)
		current.SubmissionDeadline, current.SubmissionDeadlineText, submissionChanged = mergeRegistrationDeadline(
			current.SubmissionDeadline, current.SubmissionDeadlineText, candidate.SubmissionDeadline, candidate.SubmissionDeadlineText, overwrite)
		changed = changed || deadlineChanged || submissionChanged
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
		if candidate.SubmissionDeadline != "" && current.SubmissionDeadline != "" && candidate.SubmissionDeadline != current.SubmissionDeadline {
			return fmt.Sprintf("the suggestion would change the existing registration %q submission deadline", current.Name)
		}
		if conflict, _ := compareCutoffText(current, candidate); conflict {
			return fmt.Sprintf("the suggestion would change the existing registration %q deadline", current.Name)
		}
		if conflict, _ := compareRegistrationDeadlineText(current.SubmissionDeadline, candidate.SubmissionDeadline,
			current.SubmissionDeadlineText, candidate.SubmissionDeadlineText); conflict {
			return fmt.Sprintf("the suggestion would change the existing registration %q submission deadline", current.Name)
		}
	}
	return ""
}

// deriveRegistrationDeadline chooses the nearest cutoff that is still
// applicable across both signup and submission deadlines. A future cutoff
// always wins over a past one; if every cutoff has passed, the latest past
// date is the least stale value.
func deriveRegistrationDeadline(registrations []Registration, today string) string {
	var future, past string
	consider := func(day string) {
		if day == "" {
			return
		}
		if day >= today {
			if future == "" || day < future {
				future = day
			}
			return
		}
		if past == "" || day > past {
			past = day
		}
	}
	for _, registration := range registrations {
		consider(registration.Deadline)
		consider(registration.SubmissionDeadline)
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
		"UPDATE events SET registrations = ?, deadline = ?, description = ?, updated_at = ? WHERE id = ?",
		raw, event.Deadline, event.Description, now, event.ID,
	); err != nil {
		return fmt.Errorf("update event registrations: %w", err)
	}
	day := scheduleDay(event.Start)
	if day == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE event_days SET registrations = ?, deadline = ?, description = ?, updated_at = ? WHERE event_id = ? AND day = ?",
		raw, event.Deadline, event.Description, now, event.ID, day,
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
