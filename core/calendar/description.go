package calendar

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Registration details are copied into the task description as ordinary
// editable text. The registrations column remains the verified evidence used
// to detect duplicates and reconcile later conversation updates.
const registrationDescriptionVersion = 1

const descriptionMigrationSchema = `
	CREATE TABLE IF NOT EXISTS calendar_description_versions (
		version INTEGER PRIMARY KEY
	);
`

const (
	registrationDeadlineLabelEN = "Registration deadline:"
	submissionDeadlineLabelEN   = "Submission deadline:"
	registrationDeadlineLabelZH = "报名截止："
	submissionDeadlineLabelZH   = "作品截止："
)

// FormatRegistrationBlock renders one activity as a plain paragraph. Labels
// follow the card's language, cutoff text is kept exactly as supplied, and the
// normalized date is added only when the source text does not already carry it.
func FormatRegistrationBlock(title string, registration Registration) string {
	chinese := registrationLabelsUseChinese(title, registration)
	deadlineLabel, submissionLabel := registrationDeadlineLabelEN, submissionDeadlineLabelEN
	separator := " "
	if chinese {
		deadlineLabel, submissionLabel = registrationDeadlineLabelZH, submissionDeadlineLabelZH
		separator = ""
	}

	lines := make([]string, 0, 4)
	if name := strings.TrimSpace(registration.Name); name != "" {
		lines = append(lines, name)
	}
	if cutoff := formatRegistrationCutoff(registration.Deadline, registration.DeadlineText); cutoff != "" {
		lines = append(lines, deadlineLabel+separator+cutoff)
	}
	if cutoff := formatRegistrationCutoff(registration.SubmissionDeadline, registration.SubmissionDeadlineText); cutoff != "" {
		lines = append(lines, submissionLabel+separator+cutoff)
	}
	if link := strings.TrimSpace(registration.URL); link != "" {
		lines = append(lines, link)
	}
	return strings.Join(lines, "\n")
}

// FormatRegistrationNotes renders all activities separated by a blank line.
func FormatRegistrationNotes(title string, registrations []Registration) string {
	blocks := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		if block := FormatRegistrationBlock(title, registration); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(blocks, "\n\n")
}

// ProjectDescription returns the description an accepted change would write for
// event. It is a pure projection for review UI and never changes stored values
// or the proposal.
func ProjectDescription(event Event, change ChatChange, overwrite bool) string {
	merged, _ := mergeRegistrations(event.Registrations, change.Registrations, overwrite)
	return proposedDescription(event, change, merged, overwrite)
}

// Both the review and the transaction use this projection.
func proposedDescription(event Event, change ChatChange, merged []Registration, overwrite bool) string {
	replacement := change.Description
	if replacement == event.Description || replacement == descriptionBase(event.Title, event.Description, event.Registrations) {
		replacement = ""
	}
	title := change.Title
	if title == "" {
		title = event.Title
	}
	return composeDescription(event.Title, title, event.Description, replacement, event.Registrations, merged, overwrite)
}

func registrationLabelsUseChinese(title string, registration Registration) bool {
	for _, value := range []string{
		title, registration.Name, registration.DeadlineText, registration.SubmissionDeadlineText,
	} {
		if registrationContainsHan(value) {
			return true
		}
	}
	return false
}

func formatRegistrationCutoff(day, text string) string {
	day = strings.TrimSpace(day)
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return day
	case day == "":
		return text
	case registrationTextHasDay(text, day):
		return text
	default:
		return day + " · " + text
	}
}

// registrationTextHasDay recognizes the common date spellings used in source
// cutoffs. When the exact wording already names the day, it stays untouched.
func registrationTextHasDay(text, day string) bool {
	if day == "" {
		return false
	}
	if strings.Contains(text, day) {
		return true
	}
	parsed, err := time.Parse(dateLayout, day)
	if err != nil {
		return false
	}
	compact := strings.Join(strings.Fields(text), "")
	lower := strings.ToLower(text)
	lowerCompact := strings.ToLower(compact)
	variants := []string{
		parsed.Format("2006/01/02"),
		parsed.Format("2006.01.02"),
		fmt.Sprintf("%d年%d月%d日", parsed.Year(), int(parsed.Month()), parsed.Day()),
		fmt.Sprintf("%d月%d日", int(parsed.Month()), parsed.Day()),
		fmt.Sprintf("%d/%d", int(parsed.Month()), parsed.Day()),
		fmt.Sprintf("%d-%d", int(parsed.Month()), parsed.Day()),
		fmt.Sprintf("%d.%d", int(parsed.Month()), parsed.Day()),
	}
	for _, variant := range variants {
		if strings.Contains(compact, variant) {
			return true
		}
	}
	for _, layout := range []string{"Jan 2", "January 2", "2 Jan", "2 January"} {
		variant := strings.ToLower(parsed.Format(layout))
		if strings.Contains(lower, variant) || strings.Contains(lowerCompact, strings.ReplaceAll(variant, " ", "")) {
			return true
		}
	}
	return false
}

func composeDescription(
	oldTitle, newTitle, current, replacement string,
	existing, merged []Registration,
	overwrite bool,
) string {
	next := current
	if replacement != "" {
		next = replaceDescriptionSummary(current, replacement, merged)
	}
	for index, item := range merged {
		block := FormatRegistrationBlock(newTitle, item)
		if index >= len(existing) {
			next = appendDescriptionBlock(next, block)
			continue
		}
		prior := existing[index]
		if item == prior {
			continue
		}
		oldBlock := FormatRegistrationBlock(oldTitle, prior)
		if start, end, ok := descriptionBlockBounds(next, oldBlock); ok {
			next = next[:start] + block + next[end:]
		} else if overwrite {
			next = reviseEditedActivity(next, oldTitle, newTitle, prior, item)
		}
	}
	return next
}

var paragraphBreak = regexp.MustCompile(`\r?\n[ \t]*\r?\n`)

func replaceDescriptionSummary(current, summary string, activities []Registration) string {
	if len(activities) == 0 || strings.TrimSpace(current) == "" {
		return summary
	}
	end := strings.IndexAny(current, "\r\n")
	if end < 0 {
		end = len(current)
	}
	firstLine := strings.TrimSpace(current[:end])
	for _, item := range activities {
		if firstLine == item.Name || (item.URL != "" && firstLine == item.URL) {
			return summary + "\n\n" + current
		}
	}
	return summary + current[end:]
}

// A reviewed change may update one fact in a paragraph the user rewrote.
// Apply only changed facts. Other notes and deliberately removed links stay as edited.
func reviseEditedActivity(text, oldTitle, title string, old, item Registration) string {
	type lineChange struct{ from, to string }
	var changes []lineChange
	add := func(before, after Registration) {
		from, to := FormatRegistrationBlock(oldTitle+" "+old.Name, before), FormatRegistrationBlock(title+" "+item.Name, after)
		if from != to && to != "" {
			changes = append(changes, lineChange{from, to})
		}
	}
	add(Registration{URL: old.URL}, Registration{URL: item.URL})
	if old.Deadline != item.Deadline || old.DeadlineText != item.DeadlineText {
		add(Registration{Deadline: old.Deadline, DeadlineText: old.DeadlineText},
			Registration{Deadline: item.Deadline, DeadlineText: item.DeadlineText})
	}
	if old.SubmissionDeadline != item.SubmissionDeadline || old.SubmissionDeadlineText != item.SubmissionDeadlineText {
		add(Registration{SubmissionDeadline: old.SubmissionDeadline, SubmissionDeadlineText: old.SubmissionDeadlineText},
			Registration{SubmissionDeadline: item.SubmissionDeadline, SubmissionDeadlineText: item.SubmissionDeadlineText})
	}
	if len(changes) == 0 {
		return text
	}
	for _, bounds := range paragraphBounds(text) {
		start, end := bounds[0], bounds[1]
		lines := strings.Split(strings.ReplaceAll(text[start:end], "\r\n", "\n"), "\n")
		if strings.TrimSpace(lines[0]) != old.Name {
			continue
		}
		for _, change := range changes {
			replaced := false
			for i := 1; i < len(lines); i++ {
				if (change.from != "" && lines[i] == change.from) || lines[i] == change.to {
					lines[i] = change.to
					replaced = true
					break
				}
			}
			if !replaced {
				lines = append(lines, change.to)
			}
		}
		return text[:start] + strings.Join(lines, "\n") + text[end:]
	}
	lines := []string{item.Name}
	for _, change := range changes {
		lines = append(lines, change.to)
	}
	return appendDescriptionBlock(text, strings.Join(lines, "\n"))
}

// descriptionBase removes generated blocks that still match their evidence.
// Remaining text is the editable macro summary and any manual notes.
func descriptionBase(title, description string, registrations []Registration) string {
	base := description
	for _, registration := range registrations {
		block := FormatRegistrationBlock(title, registration)
		if containsDescriptionBlock(base, block) {
			base = removeDescriptionBlock(base, block)
		}
	}
	return strings.TrimSpace(base)
}

func chatDescriptionWouldChange(before Event, change ChatChange) bool {
	proposed := strings.TrimSpace(change.Description)
	if proposed == "" || proposed == strings.TrimSpace(before.Description) {
		return false
	}
	return proposedDescription(before, change, before.Registrations, false) != before.Description
}

// registrationDescriptionConflict queues enrichment when the generated block a
// proposal would update has been removed or rewritten by the user.
func registrationDescriptionConflict(before, current Event, proposed []Registration) string {
	check := func(source Event) string {
		for _, candidate := range proposed {
			index := registrationIndex(source.Registrations, candidate)
			if index < 0 {
				continue
			}
			existing := source.Registrations[index]
			if _, changed := mergeRegistrations([]Registration{existing}, []Registration{candidate}, false); !changed {
				continue
			}
			if !containsDescriptionBlock(source.Description, FormatRegistrationBlock(source.Title, existing)) {
				return fmt.Sprintf("the suggestion would change %q activity details that were removed or edited in the description", existing.Name)
			}
		}
		return ""
	}
	if reason := check(before); reason != "" {
		return reason
	}
	return check(current)
}

func containsDescriptionBlock(description, block string) bool {
	_, _, ok := descriptionBlockBounds(description, block)
	return ok
}

func appendDescriptionBlock(description, block string) string {
	block = strings.TrimSpace(block)
	if block == "" || containsDescriptionBlock(description, block) {
		return description
	}
	if strings.TrimSpace(description) == "" {
		return block
	}
	if strings.HasSuffix(description, "\r\n\r\n") {
		return description + block
	}
	if strings.HasSuffix(description, "\r\n") {
		return description + "\r\n" + block
	}
	if strings.HasSuffix(description, "\n\n") {
		return description + block
	}
	if strings.HasSuffix(description, "\n") {
		return description + "\n" + block
	}
	return description + "\n\n" + block
}

func removeDescriptionBlock(description, block string) string {
	start, end, ok := descriptionBlockBounds(description, block)
	if !ok {
		return description
	}
	before, after := description[:start], description[end:]
	if strings.HasSuffix(before, "\n\n") && strings.HasPrefix(after, "\n\n") {
		after = after[2:]
	}
	return before + after
}

func paragraphBounds(text string) [][2]int {
	bounds := make([][2]int, 0)
	start := 0
	for _, sep := range paragraphBreak.FindAllStringIndex(text, -1) {
		bounds = append(bounds, [2]int{start, sep[0]})
		start = sep[1]
	}
	return append(bounds, [2]int{start, len(text)})
}

func descriptionBlockBounds(text, block string) (int, int, bool) {
	block = strings.TrimSpace(block)
	if block != "" {
		for _, bounds := range paragraphBounds(text) {
			raw := text[bounds[0]:bounds[1]]
			trimmed := strings.TrimSpace(raw)
			if strings.ReplaceAll(trimmed, "\r\n", "\n") == block {
				start := bounds[0] + strings.Index(raw, trimmed)
				return start, start + len(trimmed), true
			}
		}
	}
	return 0, 0, false
}

func appendRegistrationNotes(title, description string, registrations []Registration) string {
	next := description
	for _, registration := range registrations {
		next = appendDescriptionBlock(next, FormatRegistrationBlock(title, registration))
	}
	return next
}

// upgradeRegistrationDescriptions copies existing verified details into the
// editable descriptions once. The marker row commits with the same transaction.
func upgradeRegistrationDescriptions(ctx context.Context, tx *sql.Tx, today string) error {
	if _, err := tx.ExecContext(ctx, descriptionMigrationSchema); err != nil {
		return fmt.Errorf("create description migration table: %w", err)
	}
	var applied bool
	if err := tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM calendar_description_versions WHERE version = ?)",
		registrationDescriptionVersion,
	).Scan(&applied); err != nil {
		return fmt.Errorf("inspect description migration: %w", err)
	}
	if applied {
		return nil
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, '', title, description, registrations FROM events
		WHERE registrations IS NOT NULL AND trim(registrations) NOT IN ('', '[]')
		UNION ALL
		SELECT event_id, day, title, description, registrations FROM event_days
		WHERE registrations IS NOT NULL AND trim(registrations) NOT IN ('', '[]')`)
	if err != nil {
		return fmt.Errorf("read descriptions for migration: %w", err)
	}
	migrations, err := collectDescriptionMigrations(rows)
	if err != nil {
		return err
	}
	for _, row := range migrations {
		items, err := decodeRegistrations(row.registrations)
		if err != nil {
			return err
		}
		day := row.day
		if day == "" {
			day = today
		}
		orderLegacyActivities(items, day)
		text := appendRegistrationNotes(row.title, row.description, items)
		if text == row.description {
			continue
		}
		if row.day == "" {
			_, err = tx.ExecContext(ctx, "UPDATE events SET description = ? WHERE id = ?", text, row.id)
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE event_days SET description = ? WHERE event_id = ? AND day = ?", text, row.id, row.day)
		}
		if err != nil {
			return fmt.Errorf("migrate activity notes: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO calendar_description_versions (version) VALUES (?)",
		registrationDescriptionVersion,
	); err != nil {
		return fmt.Errorf("record description migration: %w", err)
	}
	return nil
}

// Preserve the order people saw in the old list on this board day. Later
// syncs only append or amend text, so the user can freely rearrange paragraphs.
func orderLegacyActivities(items []Registration, day string) {
	key := func(item Registration) (int, string) {
		cutoff := deriveRegistrationDeadline([]Registration{item}, day)
		switch {
		case cutoff == "":
			return 1, ""
		case cutoff < day:
			return 2, cutoff
		default:
			return 0, cutoff
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		groupA, dateA := key(items[i])
		groupB, dateB := key(items[j])
		if groupA != groupB {
			return groupA < groupB
		}
		if groupA == 2 {
			return dateA > dateB
		}
		return dateA < dateB
	})
}

type descriptionMigrationRow struct {
	id            int64
	day           string
	title         string
	description   string
	registrations string
}

func collectDescriptionMigrations(rows *sql.Rows) ([]descriptionMigrationRow, error) {
	defer rows.Close()
	migrations := make([]descriptionMigrationRow, 0)
	for rows.Next() {
		var migration descriptionMigrationRow
		if err := rows.Scan(&migration.id, &migration.day, &migration.title,
			&migration.description, &migration.registrations); err != nil {
			return nil, fmt.Errorf("scan description migration: %w", err)
		}
		migrations = append(migrations, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read description migrations: %w", err)
	}
	return migrations, nil
}
