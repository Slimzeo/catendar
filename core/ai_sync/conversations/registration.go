package conversations

import (
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"catendar-wails/core/calendar"
)

const (
	maxRegistrationNameRunes  = 200
	maxRegistrationURLRunes   = 2048
	maxRegistrationTextRunes  = 300
	maxRegistrationsPerChange = 12
)

type modelRegistration struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	Deadline     string   `json:"deadline"`
	DeadlineText string   `json:"deadlineText"`
	MessageIDs   []string `json:"messageIds"`
}

var (
	entryCue   = regexp.MustCompile(`(?i)报名|報名|申请|申請|入营|参赛|參賽|填表|表单|表單|register|registration|sign[ -]?up|apply|application|entry form|enrol`)
	cutoffCue  = regexp.MustCompile(`(?i)截止|截至|报名至|報名至|申请至|申請至|滚动招募|随到随审|名额满|额满|招满|deadline|cut[ -]?off|clos(?:es|ing)|apply by|due by|rolling (admission|application)|until`)
	otherDate  = regexp.MustCompile(`(?i)举办|舉辦|活动日期|活动时间|开幕|开赛|开场|举行|event (?:day|date)|takes place|starts on|opens on`)
	blockedURL = regexp.MustCompile(`(?i)^https?://(?:[^/]+\.)?(?:github\.com|gitlab\.com|paypal\.com|stripe\.com)(?:[/:]|$)|^https?://(?:news|pay|checkout)\.|/(?:payment|checkout|api)/|[?&](?:password|access_token|api_key|secret)=`)
	linkToken  = regexp.MustCompile("(?i)https?://[^\\s<>\\\"`|\\[\\]，。；！？、）】]+")
)

func validateRegistrationProposals(items *[]modelRegistration, change Message, all map[string]Message, refs map[string]string) []calendar.Registration {
	if items == nil {
		return nil
	}
	var out []calendar.Registration
	for _, item := range *items {
		name := strings.TrimSpace(item.Name)
		if name == "" || utf8.RuneCountInString(name) > maxRegistrationNameRunes || strings.ContainsAny(name, "\r\n\t") || strings.Contains(name, "://") || len(item.MessageIDs) == 0 || len(item.MessageIDs) > 8 {
			continue
		}
		var lines []registrationLine
		valid := true
		for _, id := range item.MessageIDs {
			if full, ok := refs[id]; ok {
				id = full
			}
			m, ok := all[id]
			if !ok || m.ThreadID != change.ThreadID {
				valid = false
				break
			}
			lines = append(lines, registrationLines(m, name)...)
		}
		if !valid {
			continue
		}
		r := calendar.Registration{Name: name}
		for _, line := range lines {
			if r.URL == "" && registrationURLSupported(line.text, item.URL) {
				r.URL = item.URL
			}
			if r.DeadlineText == "" && registrationCutoffSupported(line.text, item.DeadlineText) {
				r.DeadlineText = item.DeadlineText
				r.Deadline = confirmedRegistrationDay(item.DeadlineText, item.Deadline, line.at)
			}
		}
		if r.URL != "" || r.DeadlineText != "" {
			out = appendRegistrations(out, r)
		}
		if len(out) >= maxRegistrationsPerChange {
			break
		}
	}
	return out
}

type registrationLine struct {
	text string
	at   int64
}

// Restrict a fact to its named activity's row or paragraph. A quoted table
// can legitimately contain many real forms and dates, but confirming that a
// value occurs somewhere in the message is insufficient to pair it with a card.
func registrationLines(m Message, name string) []registrationLine {
	var out []registrationLine
	norm := func(s string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(s, "*", "")), ""))
	}
	want := norm(name)
	var previous string
	for _, raw := range strings.Split(m.Text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			previous = ""
			continue
		}
		named := strings.Contains(norm(line), want)
		// A named heading may immediately introduce a form on its own line. Do
		// not carry a table row's identity into the next event or across paragraphs.
		if !named && previous != "" && !strings.ContainsAny(line, "|") && strings.Contains(norm(previous), want) && !strings.Contains(previous, "://") && !cutoffCue.MatchString(previous) && utf8.RuneCountInString(previous) <= 100 && (strings.HasPrefix(line, "http") || strings.HasPrefix(line, "[") || entryCue.MatchString(line) || cutoffCue.MatchString(line)) {
			line = previous + "\n" + line
			named = true
		}
		if named {
			out = append(out, registrationLine{line, m.At})
		}
		previous = raw
		if strings.Contains(raw, "|") {
			previous = ""
		}
	}
	return out
}

func registrationURLSupported(line, raw string) bool {
	if len(raw) == 0 || len(raw) > maxRegistrationURLRunes || !entryCue.MatchString(line) || blockedURL.MatchString(raw) || strings.ContainsAny(raw, "\\ \t\r\n") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	for _, token := range linkToken.FindAllString(line, -1) {
		token = strings.TrimRight(token, ".,;:!?'")
		for strings.HasSuffix(token, ")") && strings.Count(token, ")") > strings.Count(token, "(") {
			token = strings.TrimSuffix(token, ")")
		}
		if token == raw {
			return true
		}
	}
	return false
}

func registrationCutoffSupported(line, phrase string) bool {
	if phrase == "" || utf8.RuneCountInString(phrase) > maxRegistrationTextRunes || strings.IndexFunc(phrase, unicode.IsControl) >= 0 || strings.Contains(phrase, "://") || strings.Contains(phrase, "|") {
		return false
	}
	i := strings.Index(line, phrase)
	if i < 0 {
		return false
	}
	if otherDate.MatchString(phrase) {
		return false
	}
	if cutoffCue.MatchString(phrase) {
		return true
	}
	// A model may copy just “Nov 12, 17:00”. The same clause must actually
	// call it a cutoff rather than the event day in a neighbouring cell.
	left := line[:i]
	right := line[i+len(phrase):]
	if j := strings.LastIndexAny(left, "|,，;；。.!?！？\n"); j >= 0 {
		_, n := utf8.DecodeRuneInString(left[j:])
		left = left[j+n:]
	}
	if j := strings.IndexAny(right, "|,，;；。.!?！？\n"); j >= 0 {
		right = right[:j]
	}
	clause := left + phrase + right
	return cutoffCue.MatchString(clause) && !otherDate.MatchString(clause)
}

func confirmedRegistrationDay(phrase, candidate string, at int64) string {
	mentions := registrationDateMentions(phrase)
	if len(mentions) != 1 {
		return ""
	}
	day, ok := mentions[0].canonical(time.Unix(at, 0))
	if !ok || (candidate != "" && day != candidate) {
		return ""
	}
	return day
}

// Combine partial evidence for one activity without discarding independently
// named activities during the planner's one-change-per-card deduplication.
func appendRegistrations(rows []calendar.Registration, items ...calendar.Registration) []calendar.Registration {
	for _, item := range items {
		found := false
		for i := range rows {
			r := &rows[i]
			if !strings.EqualFold(r.Name, item.Name) && !(r.URL != "" && r.URL == item.URL) {
				continue
			}
			found = true
			if r.URL == "" {
				r.URL = item.URL
			}
			if r.Deadline == "" && (r.DeadlineText == "" || r.DeadlineText == item.DeadlineText) {
				r.Deadline = item.Deadline
			}
			if r.DeadlineText == "" && (r.Deadline == "" || item.Deadline == "" || r.Deadline == item.Deadline) {
				r.DeadlineText = item.DeadlineText
			}
			break
		}
		if !found && len(rows) < maxRegistrationsPerChange {
			rows = append(rows, item)
		}
	}
	return rows
}
