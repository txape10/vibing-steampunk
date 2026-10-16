package adt

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// freestyleMaxLineRunes is the longest line wrapFreestyleSQL leaves in place.
// SAP's freestyle endpoint (CL_ADT_DP_FREESTYLE_RES=>POST) moves every line of
// the request body into a CHAR 255 row and silently drops the rest, so a long
// single-line query loses its tail — usually WHERE conditions — without any
// error. Five characters of margin below 255 are kept. The unit is runes, not
// bytes: CHAR 255 counts characters.
const freestyleMaxLineRunes = 250

// ErrFreestyleLineTooLong is returned (wrapped) when a line is longer than the
// limit and has no point where a line break is safe.
var ErrFreestyleLineTooLong = errors.New("freestyle SQL line cannot be wrapped safely")

// freestyleBreakKeywords are the words a line break is preferred in front of.
// A break before any whitespace is equally valid ABAP; the keywords only keep
// the result readable.
var freestyleBreakKeywords = map[string]bool{
	"AND": true, "OR": true, "WHERE": true, "FROM": true, "GROUP": true,
	"ORDER": true, "HAVING": true, "UNION": true, "INNER": true, "LEFT": true,
	"RIGHT": true, "CROSS": true, "JOIN": true, "ON": true, "FIELDS": true,
	"INTO": true, "UP": true, "OFFSET": true,
}

// Lower rank is preferred.
const (
	cutRankComment = iota // in front of a " comment: the comment moves whole
	cutRankKeyword        // in front of a keyword
	cutRankSpace          // at any other whitespace
	cutRankComma          // right after a comma (lists written without spaces)
)

// freestyleCut is one place a line may be broken: runes[start:end] stays on
// the current line, the next line starts at runes[next].
type freestyleCut struct {
	end  int
	next int
	rank int
}

// wrapFreestyleSQL breaks every line of sql that is longer than maxLine runes
// at a point where ABAP treats a newline like the whitespace (or comma) it
// replaces, so SAP's 255-character-per-line truncation cannot cut the query.
//
// A query whose lines are all short is returned unchanged, byte for byte.
// Lines that are already short are never touched, and CRLF line ends are kept.
func wrapFreestyleSQL(sql string, maxLine int) (string, error) {
	lines := strings.Split(sql, "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for i, line := range lines {
		body, hasCR := strings.CutSuffix(line, "\r")
		runes := []rune(body)
		if len(runes) <= maxLine {
			out = append(out, line)
			continue
		}
		// Trailing blanks carry no meaning and have no break point of their own.
		if trimmed := strings.TrimRight(body, " \t"); len([]rune(trimmed)) <= maxLine {
			if hasCR {
				trimmed += "\r"
			}
			out = append(out, trimmed)
			changed = true
			continue
		}
		segments, stuck := wrapFreestyleLine(runes, maxLine)
		if stuck > 0 {
			return "", fmt.Errorf("%w: line %d is %d characters and its last %d have no safe break point within %d "+
				"(a literal, comment or token is too long); SAP would silently truncate it at 255",
				ErrFreestyleLineTooLong, i+1, len(runes), stuck, maxLine)
		}
		for _, segment := range segments {
			if hasCR {
				segment += "\r"
			}
			out = append(out, segment)
		}
		changed = true
	}
	if !changed {
		return sql, nil
	}
	return strings.Join(out, "\n"), nil
}

// wrapFreestyleLine splits one over-long line. On failure it returns the
// number of runes left that could not be split.
func wrapFreestyleLine(runes []rune, maxLine int) (segments []string, stuck int) {
	// A * in column 1 makes the whole line a comment. SAP used to drop the
	// excess and nothing else, so cutting it here changes nothing.
	if runes[0] == '*' {
		return []string{string(runes[:maxLine])}, 0
	}

	cuts := freestyleCutCandidates(runes)
	start := 0
	for len(runes)-start > maxLine {
		best := -1
		for k, c := range cuts {
			if c.end <= start || c.end-start > maxLine || isBlankRunes(runes[start:c.end]) {
				continue
			}
			if best < 0 || c.rank < cuts[best].rank || (c.rank == cuts[best].rank && c.end > cuts[best].end) {
				best = k
			}
		}
		if best < 0 {
			// What is left is a " comment: only its excess is lost, as
			// before. Anything else is code that cannot be broken.
			if runes[start] == '"' {
				return append(segments, string(runes[start:start+maxLine])), 0
			}
			return nil, len(runes) - start
		}
		segments = append(segments, strings.TrimRight(string(runes[start:cuts[best].end]), " \t"))
		start = cuts[best].next
	}
	return append(segments, string(runes[start:])), 0
}

// freestyleCutCandidates scans the line once, tracking literals and comments,
// and lists every place a break is safe. Nothing inside '...', `...`, |...| or
// after a " is a candidate. Literals and templates cannot span lines in ABAP,
// so the scan state starts fresh on every line. Expressions embedded in a
// |...| template ({ |x| }) are not followed; ABAP SQL has no such templates,
// only the || operator, which opens and closes an empty one.
func freestyleCutCandidates(runes []rune) []freestyleCut {
	const (
		modeCode = iota
		modeQuote
		modeBacktick
		modePipe
	)
	var cuts []freestyleCut
	mode := modeCode
	n := len(runes)

	for i := 0; i < n; i++ {
		r := runes[i]
		switch mode {
		case modeQuote:
			if r == '\'' {
				if i+1 < n && runes[i+1] == '\'' {
					i++
				} else {
					mode = modeCode
				}
			}
		case modeBacktick:
			if r == '`' {
				if i+1 < n && runes[i+1] == '`' {
					i++
				} else {
					mode = modeCode
				}
			}
		case modePipe:
			if r == '\\' {
				i++
			} else if r == '|' {
				mode = modeCode
			}
		default:
			switch {
			case r == '\'':
				mode = modeQuote
			case r == '`':
				mode = modeBacktick
			case r == '|':
				mode = modePipe
			case r == '"':
				cuts = append(cuts, freestyleCut{end: i, next: i, rank: cutRankComment})
				return cuts
			case isSpaceRune(r):
				if i == 0 || isSpaceRune(runes[i-1]) {
					continue
				}
				j := skipSpaces(runes, i)
				// A line that starts with * is a comment line, so the
				// next line must not begin with the * of COUNT( * ).
				if j < n && runes[j] != '*' {
					rank := cutRankSpace
					if freestyleKeywordAt(runes, j) {
						rank = cutRankKeyword
					}
					cuts = append(cuts, freestyleCut{end: i, next: j, rank: rank})
				}
			case r == ',':
				j := skipSpaces(runes, i+1)
				if j < n && runes[j] != '*' {
					cuts = append(cuts, freestyleCut{end: i + 1, next: j, rank: cutRankComma})
				}
			}
		}
	}
	return cuts
}

func freestyleKeywordAt(runes []rune, from int) bool {
	k := from
	for k < len(runes) && unicode.IsLetter(runes[k]) {
		k++
	}
	if k == from {
		return false
	}
	if k < len(runes) && (unicode.IsDigit(runes[k]) || runes[k] == '_' || runes[k] == '~') {
		return false
	}
	return freestyleBreakKeywords[strings.ToUpper(string(runes[from:k]))]
}

func skipSpaces(runes []rune, from int) int {
	for from < len(runes) && isSpaceRune(runes[from]) {
		from++
	}
	return from
}

func isSpaceRune(r rune) bool { return r == ' ' || r == '\t' }

func isBlankRunes(runes []rune) bool {
	return skipSpaces(runes, 0) == len(runes)
}
