package notes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var momentTokens = []string{
	"YYYY", "MMMM", "dddd", "MMM", "ddd", "YY", "MM", "DD", "dd",
	"HH", "hh", "mm", "ss", "A", "M", "D", "d", "H", "h", "m", "s", "a",
}

type formatPiece struct {
	literal string
	token   string
}

func tokenizeFormat(layout string) []formatPiece {
	var pieces []formatPiece
	var literal strings.Builder

	flush := func() {
		if literal.Len() == 0 {
			return
		}
		pieces = append(pieces, formatPiece{literal: literal.String()})
		literal.Reset()
	}

	for i := 0; i < len(layout); {
		if layout[i] == '[' {
			if end := strings.IndexByte(layout[i+1:], ']'); end >= 0 {
				literal.WriteString(layout[i+1 : i+1+end])
				i += end + 2
				continue
			}
		}

		matched := false
		for _, token := range momentTokens {
			if strings.HasPrefix(layout[i:], token) {
				flush()
				pieces = append(pieces, formatPiece{token: token})
				i += len(token)
				matched = true
				break
			}
		}
		if !matched {
			literal.WriteByte(layout[i])
			i++
		}
	}

	flush()
	return pieces
}

func formatMoment(layout string, t time.Time) string {
	hour := t.Hour()
	hour12 := hour % 12
	if hour12 == 0 {
		hour12 = 12
	}

	var b strings.Builder
	for _, piece := range tokenizeFormat(layout) {
		if piece.literal != "" {
			b.WriteString(piece.literal)
			continue
		}

		switch piece.token {
		case "YYYY":
			fmt.Fprintf(&b, "%04d", t.Year())
		case "YY":
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case "MMMM":
			b.WriteString(t.Month().String())
		case "MMM":
			b.WriteString(t.Month().String()[:3])
		case "MM":
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case "M":
			fmt.Fprintf(&b, "%d", int(t.Month()))
		case "DD":
			fmt.Fprintf(&b, "%02d", t.Day())
		case "D":
			fmt.Fprintf(&b, "%d", t.Day())
		case "dddd":
			b.WriteString(t.Weekday().String())
		case "ddd":
			b.WriteString(t.Weekday().String()[:3])
		case "dd":
			b.WriteString(t.Weekday().String()[:2])
		case "d":
			fmt.Fprintf(&b, "%d", int(t.Weekday()))
		case "HH":
			fmt.Fprintf(&b, "%02d", hour)
		case "H":
			fmt.Fprintf(&b, "%d", hour)
		case "hh":
			fmt.Fprintf(&b, "%02d", hour12)
		case "h":
			fmt.Fprintf(&b, "%d", hour12)
		case "mm":
			fmt.Fprintf(&b, "%02d", t.Minute())
		case "m":
			fmt.Fprintf(&b, "%d", t.Minute())
		case "ss":
			fmt.Fprintf(&b, "%02d", t.Second())
		case "s":
			fmt.Fprintf(&b, "%d", t.Second())
		case "A":
			if hour < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case "a":
			if hour < 12 {
				b.WriteString("am")
			} else {
				b.WriteString("pm")
			}
		default:
			b.WriteString(piece.token)
		}
	}
	return b.String()
}

var (
	fullMonthNames = map[string]int{
		"january": 1, "february": 2, "march": 3, "april": 4,
		"may": 5, "june": 6, "july": 7, "august": 8,
		"september": 9, "october": 10, "november": 11, "december": 12,
	}
	shortMonthNames = map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4,
		"may": 5, "jun": 6, "jul": 7, "aug": 8,
		"sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}
)

type reverseToken struct {
	pattern string
	group   string
}

var reverseTokens = map[string]reverseToken{
	"YYYY": {pattern: `(\d{4})`, group: "year"},
	"YY":   {pattern: `(\d{2})`, group: "year2"},
	"MMMM": {pattern: `((?i:january|february|march|april|may|june|july|august|september|october|november|december))`, group: "monthName"},
	"MMM":  {pattern: `((?i:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec))`, group: "monthNameShort"},
	"MM":   {pattern: `(\d{2})`, group: "month"},
	"M":    {pattern: `(\d{1,2})`, group: "month"},
	"DD":   {pattern: `(\d{2})`, group: "day"},
	"D":    {pattern: `(\d{1,2})`, group: "day"},
	"dddd": {pattern: `(?i:[a-z]+)`, group: ""},
	"ddd":  {pattern: `(?i:[a-z]+)`, group: ""},
	"dd":   {pattern: `(?i:[a-z]+)`, group: ""},
	"d":    {pattern: `\d`, group: ""},
	"HH":   {pattern: `\d{1,2}`, group: ""},
	"H":    {pattern: `\d{1,2}`, group: ""},
	"hh":   {pattern: `\d{1,2}`, group: ""},
	"h":    {pattern: `\d{1,2}`, group: ""},
	"mm":   {pattern: `\d{1,2}`, group: ""},
	"m":    {pattern: `\d{1,2}`, group: ""},
	"ss":   {pattern: `\d{1,2}`, group: ""},
	"s":    {pattern: `\d{1,2}`, group: ""},
	"A":    {pattern: `(?i:am|pm)`, group: ""},
	"a":    {pattern: `(?i:am|pm)`, group: ""},
}

type pathDateParser struct {
	re     *regexp.Regexp
	groups []string
}

func newPathDateParser(format string) *pathDateParser {
	var b strings.Builder
	b.WriteString("^")
	var groups []string
	for _, piece := range tokenizeFormat(format) {
		if piece.literal != "" {
			b.WriteString(regexp.QuoteMeta(piece.literal))
			continue
		}
		token, ok := reverseTokens[piece.token]
		if !ok {
			b.WriteString(regexp.QuoteMeta(piece.token))
			continue
		}
		b.WriteString(token.pattern)
		if token.group != "" {
			groups = append(groups, token.group)
		}
	}
	b.WriteString("$")

	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return &pathDateParser{re: re, groups: groups}
}

func (p *pathDateParser) parse(value string) (string, bool) {
	if p == nil {
		return "", false
	}
	match := p.re.FindStringSubmatch(value)
	if match == nil {
		return "", false
	}

	year, month, day := 0, 0, 0
	seenYear, seenMonth, seenDay := false, false, false

	for i, group := range p.groups {
		value := match[i+1]
		switch group {
		case "year":
			n, err := strconv.Atoi(value)
			if err != nil || (seenYear && n != year) {
				return "", false
			}
			year, seenYear = n, true
		case "year2":
			n, err := strconv.Atoi(value)
			if err != nil {
				return "", false
			}
			if n <= 68 {
				n += 2000
			} else {
				n += 1900
			}
			if seenYear && n != year {
				return "", false
			}
			year, seenYear = n, true
		case "month":
			n, err := strconv.Atoi(value)
			if err != nil || (seenMonth && n != month) {
				return "", false
			}
			month, seenMonth = n, true
		case "monthName":
			n, ok := fullMonthNames[strings.ToLower(value)]
			if !ok || (seenMonth && n != month) {
				return "", false
			}
			month, seenMonth = n, true
		case "monthNameShort":
			n, ok := shortMonthNames[strings.ToLower(value)]
			if !ok || (seenMonth && n != month) {
				return "", false
			}
			month, seenMonth = n, true
		case "day":
			n, err := strconv.Atoi(value)
			if err != nil || (seenDay && n != day) {
				return "", false
			}
			day, seenDay = n, true
		}
	}

	if !seenYear || !seenMonth || !seenDay {
		return "", false
	}
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return "", false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return "", false
	}
	return t.Format(DateLayout), true
}

var placeholderRE = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

func renderPlaceholders(content string, noteDate, now time.Time, title string) string {
	return placeholderRE.ReplaceAllStringFunc(content, func(value string) string {
		inner := strings.TrimSpace(value[2 : len(value)-2])
		name, layout, hasLayout := strings.Cut(inner, ":")
		switch strings.TrimSpace(name) {
		case "date":
			if !hasLayout || strings.TrimSpace(layout) == "" {
				layout = "YYYY-MM-DD"
			}
			return formatMoment(strings.TrimSpace(layout), noteDate)
		case "time":
			if !hasLayout || strings.TrimSpace(layout) == "" {
				layout = "HH:mm"
			}
			return formatMoment(strings.TrimSpace(layout), now)
		case "title":
			return title
		default:
			return value
		}
	})
}
