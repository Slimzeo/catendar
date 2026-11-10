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
	maxRegistrationNameRunes = 200
	maxRegistrationURLRunes  = 2048
	maxRegistrationTextRunes = 300
)

type modelRegistration struct {
	Name                   string   `json:"name"`
	URL                    string   `json:"url"`
	Deadline               string   `json:"deadline"`
	DeadlineText           string   `json:"deadlineText"`
	SubmissionDeadline     string   `json:"submissionDeadline"`
	SubmissionDeadlineText string   `json:"submissionDeadlineText"`
	MessageIDs             []string `json:"messageIds"`
}

var (
	entryCue  = regexp.MustCompile(`(?i)报名|報名|申请|申請|入营|参赛|參賽|填表|表单|表單|register|registration|sign[ -]?up|apply|application|entry form|\bentry\b|\benter\b|enrol|报名表|報名表|报名入口|报名链接|报名网址|报名指引|报名指南|参赛指南|申请指南|官网|官網|官方(?:入口|指引|页面|網站|网站)|official (?:entry|application|registration|form|page|site|link)|instructions?|guidelines?`)
	cutoffCue = regexp.MustCompile(`(?i)截止|截至|报名至|報名至|申请至|申請至|滚动招募|随到随审|名额满|额满|招满|deadline|cut[ -]?off|clos(?:e|es|ed|ing)|apply by|due by|submit by|rolling (?:admission|application)|until|前报名|前申请|前注册|前提交|报名前|申请前|注册前|报名截止|申请截止|提交截止|register by|sign[ -]?up by|apply before|register before`)
	// Submission cutoffs are a different fact from registration cutoffs. Only
	// explicit work-submission wording may fill the submission fields.
	strongSubmissionCue = regexp.MustCompile(`(?i)交作品|作品提交|提交作品|递交作品|作品递交|交稿|截稿|封版|发布作品|作品发布|上传作品|作品上传|提交参赛作品|参赛作品提交|作品截止|作品上传截止|(?:final|project|work|entry|film|video|portfolio|artwork)\s+submission|(?:submit|submitting|submitted)\s+(?:your\s+)?(?:final\s+)?(?:work|project|entry|piece|film|video|portfolio|file|assignment|submission)|turn[ -]?in|hand[ -]?in|deliver(?:ing|ed)?\s+(?:your\s+)?(?:work|project|entry|film|video|portfolio)`)
	// Generic "submission" or "submit" is a submission only when the same
	// clause does not describe an application ("submit your application").
	weakSubmissionCue = regexp.MustCompile(`(?i)\bsubmit(?:ted|ting|s)?\b|\bsubmissions?\b|提交|递交`)
	otherDate         = regexp.MustCompile(`(?i)举办|舉辦|活动日期|活动时间|开幕|开赛|开场|举行|event (?:day|date)|takes place|starts on|opens on|\bstarts?\b|\bbegins?\b|\bopens?\b|\bkick[ -]?off\b|开始|開始|开放|開放|(?:活动|活動|比賽|比赛|赛事|会议|大會|大会|展览|演出)(?:日期|时间|時間|开始|開始|举办|舉行|举行)|决赛|決賽|颁奖|頒獎|典礼|典禮|结果(?:公布|发布)|結果(?:公布|發佈|发布)|公布结果|announce(?:s|d)? results?|results? (?:date|announced|published)|awards? ceremony|conference (?:starts?|begins?|date|schedule)|competition (?:starts?|begins?|date|schedule)|contest (?:starts?|begins?|date|schedule)|festival (?:starts?|begins?|date|schedule)|workshop (?:date|day|starts?|begins?|schedule)|(?:报名|報名|申请|申請|注册|註冊|作品提交|提交作品)(?:开始|開始|开放|開放|启动|啟動)|(?:registration|applications?|submissions?)\s+(?:opens?|open|starts?|start)|after (?:submission|submitting)|提交后|提交後`)
	blockedURL        = regexp.MustCompile(`(?i)^https?://(?:[^/]+\.)?(?:paypal\.com|stripe\.com)(?:[/:]|$)|^https?://(?:pay|checkout)\.|/(?:payment|checkout|api|blob|pulls?)/|[?&](?:password|access_token|api_key|secret)=`)
	nonEntryLabel     = regexp.MustCompile(`(?i)新闻|报道|赛程报道|往届|源码|项目仓库|项目清单|新闻稿|press release|news report|source code|gallery|results`)
	genericFollowup   = regexp.MustCompile(`(?i)^(?:https?://|报名(?:表|入口|链接|截止|时间)|申请(?:表|入口|截止)|参赛(?:入口|指南)|官方(?:报名|申请)|截止|作品提交|交稿|submission (?:deadline|form)|registration (?:form|deadline|link)|apply by|deadline|form[:：])`)

	tableSeparator  = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)+\|?\s*$`)
	factHeading     = regexp.MustCompile(`(?i)^(?:报名(?:入口|链接|表|时间|截止|条件|方式|指引)?|申请(?:入口|时间|截止|表|方式)?|官方入口|作品(?:提交|截止)|提交作品|交稿|截止(?:时间)?|registration(?: (?:deadline|link|form))?|application(?: deadline)?|submission(?: deadline)?|deadline|apply|register)$`)
	linkToken       = regexp.MustCompile("(?i)https?://[^\\s<>\\\"`|\\[\\]，。；！？、）】]+")
	markdownHeading = regexp.MustCompile(`^#{1,6}\s`)
	numberedHeading = regexp.MustCompile(`^(?:[0-9]{1,3}|第[0-9一二三四五六七八九十]+)[.)、]\s*`)
)

// A public results table can put its last activity well beyond the ordinary
// message preview. Keep its whole already-sanitized text for planning and
// evidence checks. The planner rejects oversized input before checkpointing.
func plannerMessageText(text string) string {
	if len(text) <= maxMessageBytes {
		return text
	}
	structured := strings.Count(text, "|") >= 4 || strings.Count(text, "https://")+strings.Count(text, "http://") > 1
	if structured && (entryCue.MatchString(text) || cutoffCue.MatchString(text) || strongSubmissionCue.MatchString(text)) {
		return text
	}
	return truncateUTF8(text, maxMessageBytes)
}

func validateRegistrationProposals(items *[]modelRegistration, change Message, all map[string]Message, refs map[string]string) []calendar.Registration {
	if items == nil {
		return nil
	}
	names := make([]string, 0, len(*items))
	for _, item := range *items {
		if name := strings.TrimSpace(item.Name); name != "" {
			names = append(names, name)
		}
	}
	// An unnamed form in an immediate reply can supplement one named event.
	// Arbitrary messages elsewhere in the same thread cannot.
	timeline := make([]Message, 0, len(all))
	for _, m := range all {
		if m.ThreadID == change.ThreadID {
			timeline = append(timeline, m)
		}
	}
	previous := map[string]Message{}
	var last Message
	for _, m := range orderedMessages(timeline) {
		previous[m.ID] = last
		last = m
	}
	var out []calendar.Registration
	for _, item := range *items {
		name := strings.TrimSpace(item.Name)
		if name == "" || utf8.RuneCountInString(name) > maxRegistrationNameRunes || strings.ContainsAny(name, "\r\n\t") || strings.Contains(name, "://") || len(item.MessageIDs) == 0 || len(item.MessageIDs) > 8 {
			continue
		}
		var lines []registrationLine
		nameSourced := false
		cited := map[string]bool{}
		for _, id := range item.MessageIDs {
			if full, ok := refs[id]; ok {
				id = full
			}
			cited[id] = true
		}
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
			messageLines := registrationLines(m, name, names)
			if len(messageLines) > 0 {
				nameSourced = true
			} else if prev := previous[id]; cited[prev.ID] && registrationHasName(registrationNorm(prev.Text), registrationNorm(name)) && !registrationBlockNamesOther(registrationBlock{lines: []string{prev.Text}}, registrationNorm(name), names) && !strings.Contains(prev.Text, "|") {
				// A form or cutoff can arrive in a later same-thread message
				// without repeating the event name. Keep that message linked by
				// messageIds without scanning unrelated table rows or headings.
				messageLines = registrationLooseLines(m)
			}
			lines = append(lines, messageLines...)
		}
		if !valid || !nameSourced {
			continue
		}
		r := calendar.Registration{Name: name}
		for _, line := range lines {
			if r.URL == "" && registrationURLSupported(line, item.URL, name) {
				r.URL = item.URL
			}
			if r.DeadlineText == "" && registrationCutoffSupported(line, item.DeadlineText) {
				r.DeadlineText = item.DeadlineText
				r.Deadline = confirmedRegistrationDay(item.DeadlineText, item.Deadline, line.at)
			}
			if r.SubmissionDeadlineText == "" && submissionCutoffSupported(line, item.SubmissionDeadlineText) {
				r.SubmissionDeadlineText = item.SubmissionDeadlineText
				r.SubmissionDeadline = confirmedRegistrationDay(item.SubmissionDeadlineText, item.SubmissionDeadline, line.at)
			}
		}
		if r.URL != "" || r.DeadlineText != "" || r.SubmissionDeadlineText != "" {
			out = appendRegistrations(out, r)
		}
	}
	return out
}

type registrationLine struct {
	text    string
	at      int64
	section bool
	header  string
}

type registrationBlock struct {
	lines   []string
	table   bool
	headers []string
}

// Restrict a fact to its named activity's row or paragraph. A quoted table
// can legitimately contain many real forms and dates, but confirming that a
// value occurs somewhere in the message is insufficient to pair it with a card.
func registrationLines(m Message, name string, names []string) []registrationLine {
	norm := registrationNorm
	want := norm(name)
	blocks := registrationBlocks(m.Text)
	var out []registrationLine
	seen := make(map[string]bool)
	add := func(line string, section bool, header string) {
		line = strings.TrimSpace(line)
		key := header + "\n" + line
		if line == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, registrationLine{text: line, at: m.At, section: section, header: header})
	}
	for index, block := range blocks {
		if !registrationBlockContains(block, want) {
			continue
		}
		if len(block.headers) > 0 {
			cells := tableCells(block.lines[0])
			for i, cell := range cells {
				add(cell, false, block.headers[i])
			}
			continue
		}
		section := !block.table
		for _, line := range block.lines {
			if registrationBlockNamesOther(block, want, names) && !registrationHasName(registrationNorm(line), want) {
				continue
			}
			add(line, section, "")
		}
		if block.table || !registrationBlockIsHeading(block, want) {
			continue
		}
		// Keep the whole named section, including its indented form or cutoff.
		for next := index + 1; next < len(blocks); next++ {
			block := blocks[next]
			if registrationBlockStopsSection(block) || registrationBlockNamesOther(block, want, names) {
				break
			}
			for _, line := range block.lines {
				add(line, true, "")
			}
		}
	}
	return out
}

func registrationBlocks(text string) []registrationBlock {
	var blocks []registrationBlock
	var paragraph []string
	var headers, previousRow []string
	flush := func() {
		if len(paragraph) == 0 {
			return
		}
		blocks = append(blocks, registrationBlock{lines: append([]string(nil), paragraph...)})
		paragraph = nil
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.Contains(line, "|") {
			headers, previousRow = nil, nil
		}
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "• ") || strings.HasPrefix(line, "+ ") {
			flush()
			blocks = append(blocks, registrationBlock{lines: []string{line}, table: !registrationFactLine(line)})
			continue
		}
		if strings.Contains(line, "|") {
			flush()
			cells := tableCells(line)
			if len(cells) > 1 && len(previousRow) == len(cells) && tableSeparator.MatchString(line) {
				headers = previousRow
				previousRow = nil
				continue
			}
			row := registrationBlock{lines: []string{line}, table: true}
			if len(headers) == len(cells) {
				row.headers = headers
			}
			blocks = append(blocks, row)
			previousRow = cells
			continue
		}
		if registrationMarkedHeadingLine(line) {
			flush()
			blocks = append(blocks, registrationBlock{lines: []string{line}})
			continue
		}
		paragraph = append(paragraph, line)
	}
	flush()
	return blocks
}

func registrationBlockContains(block registrationBlock, want string) bool {
	return registrationHasName(registrationNorm(strings.Join(block.lines, "\n")), want)
}

// Keep numbered names such as "Orbit 1" and "Orbit 10" distinct while
// allowing a literal Chinese event name inside surrounding Chinese prose.
func registrationHasName(text, want string) bool {
	if want == "" {
		return false
	}
	word := func(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' }
	for off := 0; off < len(text); {
		i := strings.Index(text[off:], want)
		if i < 0 {
			return false
		}
		i += off
		end := i + len(want)
		if !(i > 0 && word(text[i-1]) && word(want[0])) && !(end < len(text) && word(want[len(want)-1]) && word(text[end])) {
			return true
		}
		off = end
	}
	return false
}

func registrationBlockIsHeading(block registrationBlock, want string) bool {
	if block.table || len(block.lines) != 1 {
		return false
	}
	return registrationMarkedHeadingLine(block.lines[0]) || registrationNorm(block.lines[0]) == want
}

func tableCells(row string) []string {
	return strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
}

func registrationFactLine(line string) bool {
	label := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#*-+•>[ "))
	label = numberedHeading.ReplaceAllString(label, "")
	if i := strings.IndexAny(label, ":：]"); i >= 0 {
		label = label[:i]
	}
	return factHeading.MatchString(strings.TrimSpace(strings.Trim(label, "*"))) || genericFollowup.MatchString(label)
}

func registrationBlockStopsSection(block registrationBlock) bool {
	if block.table {
		return true
	}
	if len(block.lines) != 1 {
		return false
	}
	line := block.lines[0]
	return registrationMarkedHeadingLine(line) && !registrationFactLine(line)
}

func registrationMarkedHeadingLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return markdownHeading.MatchString(trimmed) || numberedHeading.MatchString(trimmed) ||
		strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**") && len(trimmed) > 4
}

func registrationBlockNamesOther(block registrationBlock, want string, names []string) bool {
	text := registrationNorm(strings.Join(block.lines, "\n"))
	for _, name := range names {
		other := registrationNorm(name)
		if other != "" && other != want && registrationHasName(text, other) {
			return true
		}
	}
	return false
}

func registrationNorm(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(value, "*", "")), " "))
}

// When the cited message never repeats the event name, only a short message
// made of actionable lines is safe to use. Tables and headings may describe
// several events, so they must not leak into an unrelated card.
func registrationLooseLines(m Message) []registrationLine {
	rawLines := strings.Split(m.Text, "\n")
	if utf8.RuneCountInString(m.Text) > 1000 {
		return nil
	}
	for _, raw := range rawLines {
		line := strings.TrimSpace(raw)
		if line != "" && (!genericFollowup.MatchString(line) || strings.Contains(line, "|") || registrationMarkedHeadingLine(line)) {
			return nil
		}
	}
	var out []registrationLine
	for _, raw := range rawLines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if entryCue.MatchString(line) || cutoffCue.MatchString(line) || strongSubmissionCue.MatchString(line) || weakSubmissionCue.MatchString(line) || len(linkToken.FindAllString(line, -1)) > 0 {
			out = append(out, registrationLine{text: line, at: m.At, section: true})
		}
	}
	return out
}

func registrationURLSupported(line registrationLine, raw, name string) bool {
	if len(raw) == 0 || len(raw) > maxRegistrationURLRunes || blockedURL.MatchString(raw) || strings.ContainsAny(raw, "\\ \t\r\n") {
		return false
	}
	namedLink := "[" + registrationNorm(name) + "](" + strings.ToLower(raw) + ")"
	if !entryCue.MatchString(line.header+" "+line.text) && !strings.Contains(registrationNorm(line.text), namedLink) && !(line.section && registrationBareURL(line.text, raw)) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	for _, span := range linkToken.FindAllStringIndex(line.text, -1) {
		token := line.text[span[0]:span[1]]
		if prefix := line.text[:span[0]]; strings.HasSuffix(prefix, "](") {
			if bracket := strings.LastIndex(prefix, "["); bracket >= 0 {
				label := prefix[bracket+1 : len(prefix)-2]
				if nonEntryLabel.MatchString(label) && !entryCue.MatchString(label) {
					continue
				}
			}
		}
		token = strings.TrimRight(token, ".,;:!?'*")
		for strings.HasSuffix(token, ")") && strings.Count(token, ")") > strings.Count(token, "(") {
			token = strings.TrimSuffix(token, ")")
		}
		if token == raw {
			return true
		}
	}
	return false
}

// A paragraph under the event's own heading may be just the form URL.
func registrationBareURL(line, raw string) bool {
	value := strings.TrimSpace(strings.TrimLeft(line, "-*•> "))
	value = strings.TrimSpace(strings.TrimRight(value, "。.,，;；:：*"))
	return value == raw
}

func registrationCutoffSupported(line registrationLine, phrase string) bool {
	if !registrationTextValid(phrase) {
		return false
	}
	clause, ok := registrationClause(line.text, phrase)
	clause = line.header + " " + clause
	if !ok || registrationRejectsOtherDate(phrase, clause) || !cutoffCue.MatchString(clause) {
		return false
	}
	// "作品提交截止 12 月 9 日" is a submission cutoff, not registration.
	// "报名和发布作品同时截止" explicitly covers both.
	if submissionCueIn(clause) && !entryCue.MatchString(clause) {
		return false
	}
	return true
}

func submissionCutoffSupported(line registrationLine, phrase string) bool {
	if !registrationTextValid(phrase) {
		return false
	}
	clause, ok := registrationClause(line.text, phrase)
	clause = line.header + " " + clause
	if !ok || registrationRejectsOtherDate(phrase, clause) {
		return false
	}
	strong := strongSubmissionCue.MatchString(clause)
	application := entryCue.MatchString(clause)
	if !strong && !(weakSubmissionCue.MatchString(clause) && !application) {
		return false
	}
	// A submission phrase without a date is only instructions, not a cutoff.
	return len(registrationDateMentions(clause)) > 0
}

func submissionCueIn(clause string) bool {
	return strongSubmissionCue.MatchString(clause) || weakSubmissionCue.MatchString(clause)
}

func registrationTextValid(phrase string) bool {
	return phrase != "" &&
		utf8.RuneCountInString(phrase) <= maxRegistrationTextRunes &&
		strings.IndexFunc(phrase, unicode.IsControl) < 0 &&
		!strings.Contains(phrase, "://") &&
		!strings.Contains(phrase, "|")
}

func registrationClause(line, phrase string) (string, bool) {
	i := strings.Index(line, phrase)
	if i < 0 {
		return "", false
	}
	left, right := line[:i], line[i+len(phrase):]
	if j := strings.LastIndexAny(left, "|,，;；。.!?！？\n"); j >= 0 {
		_, n := utf8.DecodeRuneInString(left[j:])
		left = left[j+n:]
	}
	if j := strings.IndexAny(right, "|,，;；。.!?！？\n"); j >= 0 {
		right = right[:j]
	}
	return left + phrase + right, true
}

func registrationRejectsOtherDate(phrase, clause string) bool {
	if otherDate.MatchString(phrase) {
		return true
	}
	if !otherDate.MatchString(clause) {
		return false
	}
	return !cutoffCue.MatchString(phrase) && !entryCue.MatchString(phrase) &&
		!strongSubmissionCue.MatchString(phrase) && !weakSubmissionCue.MatchString(phrase)
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
// named activities. A shared generic form URL is not enough to merge rows.
func appendRegistrations(rows []calendar.Registration, items ...calendar.Registration) []calendar.Registration {
	for _, item := range items {
		index := -1
		for i := range rows {
			if strings.EqualFold(strings.TrimSpace(rows[i].Name), strings.TrimSpace(item.Name)) {
				index = i
				break
			}
		}
		if index < 0 {
			rows = append(rows, item)
			continue
		}
		r := &rows[index]
		if r.URL == "" {
			r.URL = item.URL
		}
		if r.Deadline == "" && (r.DeadlineText == "" || r.DeadlineText == item.DeadlineText) {
			r.Deadline = item.Deadline
		}
		if r.DeadlineText == "" && (r.Deadline == "" || item.Deadline == "" || r.Deadline == item.Deadline) {
			r.DeadlineText = item.DeadlineText
		}
		if r.SubmissionDeadline == "" && (r.SubmissionDeadlineText == "" || r.SubmissionDeadlineText == item.SubmissionDeadlineText) {
			r.SubmissionDeadline = item.SubmissionDeadline
		}
		if r.SubmissionDeadlineText == "" && (r.SubmissionDeadline == "" || item.SubmissionDeadline == "" || r.SubmissionDeadline == item.SubmissionDeadline) {
			r.SubmissionDeadlineText = item.SubmissionDeadlineText
		}
	}
	return rows
}
