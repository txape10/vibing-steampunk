package saprfc

import (
	"strings"
	"testing"
)

func TestSplitWhereClause(t *testing.T) {
	cases := []struct {
		name  string
		where string
		want  int // expected number of OPTIONS rows
	}{
		{"empty", "", 0},
		{"short", "MANDT = '001'", 1},
		{"exactly 72", strings.Repeat("A", 71) + "B", 1},
		{"73 chars", "FUNCNAME LIKE 'RFC_READ%' OR FUNCNAME LIKE 'RFC_PING%' OR FUNCNAME LIKE 'STFC%'", 2},
		{"long", "FUNCNAME LIKE 'RFC_READ%' OR FUNCNAME LIKE 'RFC_PING%' OR FUNCNAME LIKE 'STFC%' OR FUNCNAME LIKE 'BAPI_USER%' AND FMODE = 'R'", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitWhereClause(tc.where)
			if err != nil {
				t.Fatalf("splitWhereClause(%q): %v", tc.where, err)
			}
			if len(got) != tc.want {
				t.Errorf("got %d rows, want %d: %q", len(got), tc.want, got)
			}
			for i, line := range got {
				if len(line) > optionsLineLen {
					t.Errorf("row %d is %d chars (max %d): %q", i, len(line), optionsLineLen, line)
				}
				if line != strings.TrimSpace(line) {
					t.Errorf("row %d has stray edge whitespace: %q", i, line)
				}
			}
			// ABAP joins the OPTIONS rows with a blank; the clause must come
			// back identical (token for token, in order).
			if rejoined := strings.Join(got, " "); rejoined != strings.Join(strings.Fields(tc.where), " ") {
				t.Errorf("rejoined %q != original %q", rejoined, tc.where)
			}
		})
	}
}

// A clause packed with long tokens must still keep every token whole.
func TestSplitWhereClauseNeverSplitsAToken(t *testing.T) {
	// Long, awkward tokens are the point of this case; the names are synthetic
	// on purpose, because a real logon name in a tracked test is a leak.
	where := "BNAME LIKE 'DEVELOPER%' OR BNAME LIKE 'TESTUSER_LONGNAME%' OR BNAME LIKE 'ZDEMO_SVCUSER%' OR BNAME = 'DDIC'"
	rows, err := splitWhereClause(where)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected the clause to be split, got %d row(s)", len(rows))
	}
	var seen []string
	for _, r := range rows {
		if len(r) > optionsLineLen {
			t.Errorf("row too long (%d): %q", len(r), r)
		}
		seen = append(seen, strings.Fields(r)...)
	}
	want := strings.Fields(where)
	if len(seen) != len(want) {
		t.Fatalf("token count %d != %d", len(seen), len(want))
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("token %d: got %q, want %q", i, seen[i], want[i])
		}
	}
}

// A single token wider than the OPTIONS line cannot be expressed; that must be
// an error rather than a silent truncation.
func TestSplitWhereClauseRejectsOversizedToken(t *testing.T) {
	where := "BNAME = '" + strings.Repeat("X", 80) + "' OR MANDT = '001'"
	if _, err := splitWhereClause(where); err == nil {
		t.Fatal("expected an error for a token longer than the OPTIONS line")
	}
}
