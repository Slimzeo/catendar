package conversations

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type registrationDateMention struct {
	start   int
	end     int
	year    int
	month   int
	day     int
	hasYear bool
}

var (
	registrationISODatePattern      = regexp.MustCompile(`\b([0-9]{4})[-/.]([0-9]{1,2})[-/.]([0-9]{1,2})`)
	registrationYearLastDatePattern = regexp.MustCompile(`\b([0-9]{1,2})/([0-9]{1,2})/([0-9]{4})`)
	registrationChineseDatePattern  = regexp.MustCompile(`([0-9]{4})\s*年\s*([0-9]{1,2})\s*月\s*([0-9]{1,2})\s*[日号]?`)
	registrationEnglishDatePattern  = regexp.MustCompile(`(?i)\b(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:tember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?\s+([0-9]{1,2})(?:st|nd|rd|th)?(?:,?\s*([0-9]{4}))?`)
	registrationMonthDayPattern     = regexp.MustCompile(`\b([0-9]{1,2})/([0-9]{1,2})`)
	registrationChineseMonthDay     = regexp.MustCompile(`([0-9]{1,2})\s*月\s*([0-9]{1,2})\s*[日号]?`)
)

var registrationEnglishMonths = map[string]int{
	"jan": 1, "january": 1, "feb": 2, "february": 2, "mar": 3, "march": 3,
	"apr": 4, "april": 4, "may": 5, "jun": 6, "june": 6, "jul": 7, "july": 7,
	"aug": 8, "august": 8, "sep": 9, "september": 9, "oct": 10, "october": 10,
	"nov": 11, "november": 11, "dec": 12, "december": 12,
}

func registrationDateMentions(text string) []registrationDateMention {
	mentions := make([]registrationDateMention, 0, 2)
	occupied := make([][2]int, 0, 2)
	add := func(start, end, year, month, day int, hasYear bool) {
		for _, span := range occupied {
			if start < span[1] && end > span[0] {
				return
			}
		}
		occupied = append(occupied, [2]int{start, end})
		if !registrationValidDateParts(year, month, day, hasYear) {
			return
		}
		mentions = append(mentions, registrationDateMention{
			start: start, end: end, year: year, month: month, day: day, hasYear: hasYear,
		})
	}

	for _, match := range registrationISODatePattern.FindAllStringSubmatchIndex(text, -1) {
		if registrationDigitAt(text, match[1]) {
			continue
		}
		add(match[0], match[1],
			registrationAtoi(text, match[2], match[3]),
			registrationAtoi(text, match[4], match[5]),
			registrationAtoi(text, match[6], match[7]), true)
	}
	for _, match := range registrationYearLastDatePattern.FindAllStringSubmatchIndex(text, -1) {
		if registrationDigitAt(text, match[1]) {
			continue
		}
		first := registrationAtoi(text, match[2], match[3])
		second := registrationAtoi(text, match[4], match[5])
		month, day := 0, 0
		switch {
		case first > 12 && second <= 12:
			day, month = first, second
		case second > 12 && first <= 12:
			month, day = first, second
		}
		add(match[0], match[1], registrationAtoi(text, match[6], match[7]), month, day, true)
	}
	for _, match := range registrationChineseDatePattern.FindAllStringSubmatchIndex(text, -1) {
		if !registrationChineseDayMarker(text[match[0]:match[1]]) && registrationDigitAt(text, match[1]) {
			continue
		}
		add(match[0], match[1],
			registrationAtoi(text, match[2], match[3]),
			registrationAtoi(text, match[4], match[5]),
			registrationAtoi(text, match[6], match[7]), true)
	}
	for _, match := range registrationEnglishDatePattern.FindAllStringSubmatchIndex(text, -1) {
		if registrationDigitAt(text, match[1]) {
			continue
		}
		month, ok := registrationEnglishMonths[strings.ToLower(text[match[2]:match[3]])]
		if !ok {
			continue
		}
		year, hasYear := 0, false
		if match[6] >= 0 {
			year, hasYear = registrationAtoi(text, match[6], match[7]), true
		}
		add(match[0], match[1], year, month, registrationAtoi(text, match[4], match[5]), hasYear)
	}
	for _, match := range registrationMonthDayPattern.FindAllStringSubmatchIndex(text, -1) {
		if registrationDigitAt(text, match[1]) {
			continue
		}
		first := registrationAtoi(text, match[2], match[3])
		second := registrationAtoi(text, match[4], match[5])
		month, day := 0, 0
		switch {
		case first > 12 && second <= 12:
			day, month = first, second
		case second > 12 && first <= 12:
			month, day = first, second
		}
		add(match[0], match[1], 0, month, day, false)
	}
	for _, match := range registrationChineseMonthDay.FindAllStringSubmatchIndex(text, -1) {
		if !registrationChineseDayMarker(text[match[0]:match[1]]) && registrationDigitAt(text, match[1]) {
			continue
		}
		add(match[0], match[1], 0,
			registrationAtoi(text, match[2], match[3]),
			registrationAtoi(text, match[4], match[5]), false)
	}
	return mentions
}

func registrationDigitAt(text string, index int) bool {
	return index < len(text) && text[index] >= '0' && text[index] <= '9'
}

func registrationChineseDayMarker(value string) bool {
	return strings.HasSuffix(value, "日") || strings.HasSuffix(value, "号")
}

func registrationAtoi(text string, start, end int) int {
	value, _ := strconv.Atoi(text[start:end])
	return value
}

func registrationValidDateParts(year, month, day int, hasYear bool) bool {
	if !hasYear {
		year = 2000
	}
	if year < 1 || year > 9999 || month < 1 || month > 12 || day < 1 || day > 31 {
		return false
	}
	candidate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return candidate.Year() == year && int(candidate.Month()) == month && candidate.Day() == day
}

func (mention registrationDateMention) canonical(source time.Time) (string, bool) {
	year := mention.year
	if !mention.hasYear {
		year = source.Year()
		if mention.month < int(source.Month()) ||
			(mention.month == int(source.Month()) && mention.day < source.Day()) {
			// The same month/day already passed in the source year, so the
			// year is genuinely ambiguous. Keep the wording but no ISO date.
			return "", false
		}
	}
	if !registrationValidDateParts(year, mention.month, mention.day, true) {
		return "", false
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, mention.month, mention.day), true
}

var (
	chineseCutoffKeywords = []string{
		"截止", "截至", "报名到", "报名至", "申请到", "申请至", "注册到", "注册至",
		"前完成", "前提交", "前报名", "前申请", "前注册",
	}
	englishCutoffWords = []string{
		"ddl", "deadline", "due", "closes", "close", "closed", "ends",
		"end date", "last day", "last date", "final day", "final date",
		"submit by", "submitted by", "apply by", "register by",
		"registration closes", "applications close", "no later than", "cutoff", "cut-off",
	}
)
