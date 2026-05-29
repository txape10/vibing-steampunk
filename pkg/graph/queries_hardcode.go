package graph

import "sort"

// HardcodeEntryStatus classifies each ZTCA_HARDCODE entry based on how its
// SUBKEYFLD value relates to the programs that actually call it.
type HardcodeEntryStatus string

const (
	// StatusStandard: SUBKEYFLD matches exactly one caller and that caller
	// confirms the field in its source (sy-repid pattern, HIGH confidence).
	StatusStandard HardcodeEntryStatus = "STANDARD"

	// StatusReuse: SUBKEYFLD maps to one program but other programs also call
	// this entry (intentional reuse — one entry shared across programs).
	StatusReuse HardcodeEntryStatus = "REUSE"

	// StatusMisconfigured: the value in SUBKEYFLD does not match any of the
	// programs that actually call this entry (wrong program recorded).
	StatusMisconfigured HardcodeEntryStatus = "MISCONFIGURED"

	// StatusDead: no program was found that calls this entry at all.
	StatusDead HardcodeEntryStatus = "DEAD"

	// StatusDynamic: entry is used but the subkey is built dynamically at
	// runtime (could not be confirmed via grep — MEDIUM confidence only).
	StatusDynamic HardcodeEntryStatus = "DYNAMIC"
)

// HardcodeCaller is a program or class that calls ZCL_GET_HARDCODE or selects
// ZTCA_HARDCODE directly. It carries the evidence used for classification.
type HardcodeCaller struct {
	ObjectType string `json:"object_type"` // CLAS, PROG, FUGR, …
	ObjectName string `json:"object_name"`
	Package    string `json:"package,omitempty"`
	Confidence string `json:"confidence"` // HIGH (grep-confirmed) | MEDIUM (CROSS-only)
	// CallsDirectSQL is true when the caller does SELECT directly on ZTCA_HARDCODE
	// (detected via CROSS TYPE='DA'), rather than going through ZCL_GET_HARDCODE.
	CallsDirectSQL bool `json:"calls_direct_sql,omitempty"`
	// CallsAccessor is true when the caller references ZCL_GET_HARDCODE.
	CallsAccessor bool `json:"calls_accessor,omitempty"`
}

// HardcodeEntry represents one unique (AREA, KEYFLD, KEYVAL, SUBKEYFLD, FIELD)
// combination from ZTCA_HARDCODE, enriched with usage analysis.
type HardcodeEntry struct {
	// Key fields from ZTCA_HARDCODE
	Area      string `json:"area"`
	KeyFld    string `json:"keyfld"`
	KeyVal    string `json:"keyval"`
	SubKeyFld string `json:"subkeyfld"` // Expected caller program (sy-repid convention)
	Field     string `json:"field"`

	// Analysis result
	Status  HardcodeEntryStatus `json:"status"`
	Callers []HardcodeCaller    `json:"callers"` // Programs that actually use this entry

	// SubKeyFldMatch: true when at least one caller matches SUBKEYFLD exactly.
	SubKeyFldMatch bool `json:"subkeyfld_match"`

	// Notes for display / documentation
	Note string `json:"note,omitempty"`
}

// HardcodeUsageResult is the output of a hardcode usage query for a specific
// FIELD or PROGRAM filter.
type HardcodeUsageResult struct {
	Filter  string          `json:"filter"`  // What was searched (field name or program)
	Found   bool            `json:"found"`   // Whether any entries were found
	Entries []HardcodeEntry `json:"entries"` // Matching ZTCA_HARDCODE entries with usage
}

// HardcodeAuditResult is the full system-wide audit of ZTCA_HARDCODE.
type HardcodeAuditResult struct {
	TotalEntries     int `json:"total_entries"`
	StandardCount    int `json:"standard_count"`
	ReuseCount       int `json:"reuse_count"`
	MisconfiguredCount int `json:"misconfigured_count"`
	DeadCount        int `json:"dead_count"`
	DynamicCount     int `json:"dynamic_count"`

	Entries []HardcodeEntry `json:"entries"`
}

// SortHardcodeEntries sorts entries by (Area, Field, KeyFld, KeyVal, SubKeyFld)
// for stable, deterministic output.
func SortHardcodeEntries(entries []HardcodeEntry) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Area != b.Area {
			return a.Area < b.Area
		}
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.KeyFld != b.KeyFld {
			return a.KeyFld < b.KeyFld
		}
		if a.KeyVal != b.KeyVal {
			return a.KeyVal < b.KeyVal
		}
		return a.SubKeyFld < b.SubKeyFld
	})
}

// ClassifyEntry sets Status and SubKeyFldMatch on a HardcodeEntry based on
// its callers list and the SUBKEYFLD value.
func ClassifyEntry(e *HardcodeEntry) {
	if len(e.Callers) == 0 {
		e.Status = StatusDead
		return
	}

	// Check if any caller has HIGH confidence
	hasHigh := false
	for _, c := range e.Callers {
		if c.Confidence == "HIGH" {
			hasHigh = true
			break
		}
	}
	if !hasHigh {
		// All callers are MEDIUM (CROSS-only, no grep confirmation) → dynamic usage
		e.Status = StatusDynamic
		return
	}

	// Check if SUBKEYFLD matches any confirmed caller
	for _, c := range e.Callers {
		if c.ObjectName == e.SubKeyFld && c.Confidence == "HIGH" {
			e.SubKeyFldMatch = true
			break
		}
	}

	switch {
	case !e.SubKeyFldMatch:
		// No caller matched SUBKEYFLD — misconfigured entry
		e.Status = StatusMisconfigured
	case len(e.Callers) == 1:
		// Exactly one caller and it matches SUBKEYFLD
		e.Status = StatusStandard
	default:
		// Multiple callers: SUBKEYFLD matches one, others are extras → reuse
		e.Status = StatusReuse
	}
}

// StatusEmoji returns a display emoji for the entry status, used in text output.
func StatusEmoji(s HardcodeEntryStatus) string {
	switch s {
	case StatusStandard:
		return "OK"
	case StatusReuse:
		return "INFO"
	case StatusMisconfigured:
		return "WARN"
	case StatusDead:
		return "DEAD"
	case StatusDynamic:
		return "DYN"
	default:
		return "?"
	}
}
