package adt

import (
	"regexp"
	"strings"
)

// Job identifiers reach SQL text (freestyle SQL and RFC_READ_TABLE OPTIONS have no
// bind parameters), so every caller-supplied value goes through these whitelists
// first. They reject instead of escaping.

// ValidJobName reports whether name is safe to interpolate literally into a SQL
// WHERE clause. TBTCO-JOBNAME is CHAR 32; nothing in the accepted set can close a
// quoted literal early.
func ValidJobName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '/':
		default:
			return false
		}
	}
	return true
}

var jobNamePatternRe = regexp.MustCompile(`^[A-Za-z0-9_\-/*+]{1,32}$`)

// ValidJobNamePattern is ValidJobName plus the generic-selection characters `*`
// and `+`, for filters such as "VSP_*".
func ValidJobNamePattern(p string) bool {
	return jobNamePatternRe.MatchString(p)
}

// ValidJobCount reports whether count is a well-formed TBTCO-JOBCOUNT (an
// 8-digit NUMC field).
func ValidJobCount(count string) bool {
	if count == "" || len(count) > 8 {
		return false
	}
	for _, r := range count {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// JobStatusText turns a TBTCO status letter into words.
func JobStatusText(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "P":
		return "scheduled"
	case "S":
		return "released"
	case "R":
		return "running"
	case "F":
		return "finished"
	case "A":
		return "cancelled"
	case "Y":
		return "ready"
	case "":
		return ""
	}
	return "unknown"
}
