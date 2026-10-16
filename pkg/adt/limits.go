package adt

// UnlimitedRows is the practical sentinel sent to SAP's ADT datapreview
// endpoints when a caller asks for "all rows" (rowNumber has no true
// unlimited value). This is an approximation, not a true unlimited — see
// issue #163.
//
// 90,000 is not an arbitrary round number: a higher value silently fails.
// Binary search against a live system (2026-08-31, same 916-row TADIR query
// used throughout this issue) found rowNumber values up to and including
// 100,000 return the full result set, while 150,000 and everything tried
// above it (200,000; 500,000; 999,999; 1,000,000; 10,000,000) silently
// truncated to ~100 rows instead — no error, just fewer rows, exactly like
// the original bug. A follow-up regression narrowed the window further:
// callers can add an offset on top of this sentinel (see
// handleGetTableContents's fetchRows composition), and 100,900 (100,000 +
// an observed offset of 900) already fell back into the same silent
// truncation — so the real cutoff sits somewhere between 100,000 and
// 100,900, tighter than the first pass found. This is not a 6-digit
// field-width limit (999,999 fails too); it's a hard cutoff inside the
// datapreview/ddic ADT endpoint itself, outside vsp's control, and its
// exact position may vary by system. 90,000 keeps a real safety margin
// below the narrowest confirmed-working point rather than hugging it —
// deliberately not "as large as possible", since a bigger number here
// reintroduces exactly the silent truncation this constant exists to
// avoid. (internal/mcp/handlers_read.go's handleGetTableContents also no
// longer adds offset on top of this sentinel for the all_rows path, for the
// same reason.)
const UnlimitedRows = 90_000

// ResolveRowLimit turns a caller's requested row count into the value to
// send as SAP ADT's rowNumber. wantsUnlimited means the caller explicitly
// asked for "all rows", which maps to UnlimitedRows; how a caller signals
// that is caller-specific:
//   - the CLI uses --top 0, since cobra's Changed("top") distinguishes
//     "typed 0" from "flag left at its zero default" in-process.
//   - the MCP layer uses a separate all_rows: true boolean, NOT a special
//     value of max_rows. Verified live (2026-08-31): neither max_rows: 0
//     nor max_rows: -1 reached the server as their literal value — both
//     landed identically to max_rows being omitted entirely, while every
//     positive value (7, 5000, ...) worked correctly, including against the
//     same ddic datapreview endpoint. That rules out a transport-wide
//     "drops the key" bug (a present key with a real value came through
//     fine for positive numbers) and an endpoint-side row cap (5000 came
//     back in full). What's left is that some layer between the caller and
//     this process normalizes a non-positive number-typed argument toward
//     "absent" before serialization — a boolean has no zero-like value to
//     normalize toward, so all_rows sidesteps the problem instead of
//     guessing at another magic number. See issue #163.
func ResolveRowLimit(wantsUnlimited bool, requested int) int {
	switch {
	case wantsUnlimited:
		return UnlimitedRows
	case requested > 0:
		return requested
	default:
		return 100
	}
}
