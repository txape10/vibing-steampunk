package adt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// The query that, on one line, came back from SAP with its last condition
// silently missing (558383 with and without it; 253952 once split by hand).
const freestyleQuery275 = "SELECT COUNT( * ) FROM ztsu_vales AS tv INNER JOIN ztsu_partidas AS tp ON tv~numvale = tp~numvale " +
	"WHERE tv~bukrs = '1000' AND tv~status <> 'CANC' AND tp~status <> 'CANC' " +
	"AND tv~fecha_subasta BETWEEN '20230901' AND '20240831' AND tv~idvale_tipo = 'V' AND tv~intercentro <> 'X'"

func maxLineRunes(s string) int {
	longest := 0
	for _, line := range strings.Split(s, "\n") {
		if n := utf8.RuneCountInString(strings.TrimSuffix(line, "\r")); n > longest {
			longest = n
		}
	}
	return longest
}

func TestWrapFreestyleSQL_ShortQueriesAreUntouched(t *testing.T) {
	for name, sql := range map[string]string{
		"empty":                "",
		"one line":             "SELECT * FROM t000",
		"multiline":            "SELECT *\nFROM t000\nWHERE mandt = '100'",
		"crlf":                 "SELECT *\r\nFROM t000\r\nWHERE mandt = '100'",
		"exactly at the limit": strings.Repeat("a", freestyleMaxLineRunes),
	} {
		got, err := wrapFreestyleSQL(sql, freestyleMaxLineRunes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != sql {
			t.Errorf("%s: query changed\n got: %q\nwant: %q", name, got, sql)
		}
	}
}

func TestWrapFreestyleSQL_RealQueryBreaksWhereTheManualFixDid(t *testing.T) {
	if n := utf8.RuneCountInString(freestyleQuery275); n != 275 {
		t.Fatalf("fixture is %d runes, expected 275", n)
	}
	got, err := wrapFreestyleSQL(freestyleQuery275, freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(freestyleQuery275, " AND tv~intercentro", "\nAND tv~intercentro", 1)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestWrapFreestyleSQL_NeverCutsInsideLiterals(t *testing.T) {
	// The literal starts near column 175 and ends near 275, so it crosses the
	// limit with AND/OR/WHERE and spaces inside the window: an automaton that
	// did not follow literals would cut there.
	filler := strings.Repeat("x", 150)
	pad := strings.Repeat("y", 80)
	for name, literal := range map[string]string{
		"quote":              "'a AND b OR c WHERE d " + pad + "'",
		"quote escape":       "'it''s a AND b OR c " + pad + "'",
		"quote with dquote":  `'say "hi" AND bye ` + pad + `'`,
		"backtick":           "`a AND b `` OR c " + pad + "`",
		"template":           `|a AND b \| OR c ` + pad + `|`,
		"concat operator":    "'a' || 'b AND c OR d " + pad + "'",
		"tab inside literal": "'a\tAND b OR c " + pad + "'",
	} {
		sql := "SELECT " + filler + " FROM t WHERE a = " + literal + " AND b = 1"
		got, err := wrapFreestyleSQL(sql, freestyleMaxLineRunes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(got, literal) {
			t.Errorf("%s: literal %q was cut:\n%s", name, literal, got)
		}
		if maxLineRunes(got) > freestyleMaxLineRunes {
			t.Errorf("%s: a line is still too long:\n%s", name, got)
		}
	}
}

func TestWrapFreestyleSQL_LiteralLongerThanTheLimitIsAnError(t *testing.T) {
	sql := "SELECT * FROM t WHERE a = '" + strings.Repeat("x", 300) + "'"
	_, err := wrapFreestyleSQL(sql, freestyleMaxLineRunes)
	if !errors.Is(err, ErrFreestyleLineTooLong) {
		t.Fatalf("got %v, want ErrFreestyleLineTooLong", err)
	}
}

func TestWrapFreestyleSQL_ListWithoutSpacesBreaksAfterAComma(t *testing.T) {
	var items []string
	for i := 0; i < 40; i++ {
		items = append(items, "'ZTSU_OBJETO_"+strings.Repeat("A", 5)+"'")
	}
	sql := "SELECT COUNT( * ) FROM tadir WHERE obj_name IN (" + strings.Join(items, ",") + ")"
	got, err := wrapFreestyleSQL(sql, freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	if maxLineRunes(got) > freestyleMaxLineRunes {
		t.Errorf("a line is still too long:\n%s", got)
	}
	if strings.Join(strings.Fields(got), "") != strings.Join(strings.Fields(sql), "") {
		t.Error("wrapping changed something other than whitespace")
	}
	// The items have no spaces between them, so the tail can only be broken
	// after commas.
	if !strings.Contains(got, ",\n'") {
		t.Errorf("expected a break right after a comma:\n%s", got)
	}
}

func TestWrapFreestyleSQL_TokenWithNoBreakPointIsAnError(t *testing.T) {
	_, err := wrapFreestyleSQL("SELECT "+strings.Repeat("a", 300), freestyleMaxLineRunes)
	if !errors.Is(err, ErrFreestyleLineTooLong) {
		t.Fatalf("got %v, want ErrFreestyleLineTooLong", err)
	}
	_, err = wrapFreestyleSQL(strings.Repeat("a", 300), freestyleMaxLineRunes)
	if !errors.Is(err, ErrFreestyleLineTooLong) {
		t.Fatalf("got %v, want ErrFreestyleLineTooLong", err)
	}
}

func TestWrapFreestyleSQL_NeverStartsALineWithAStar(t *testing.T) {
	// "COUNT( * )": a line that began with * would be a comment line. The
	// blank in front of the * sits at column 249, inside the window, and is
	// the rightmost blank there, so only the guard keeps it from being used.
	sql := "SELECT " + strings.Repeat("a", 235) + " COUNT( * ) FROM t"
	got, err := wrapFreestyleSQL(sql, freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "*") {
			t.Errorf("line starts with *:\n%s", got)
		}
	}
}

func TestWrapFreestyleSQL_NeverBreaksAfterACommaIntoAStar(t *testing.T) {
	// No blank anywhere, so the comma is the only candidate, and what follows
	// it is a *: no safe break exists.
	_, err := wrapFreestyleSQL(strings.Repeat("a", 240)+",*"+strings.Repeat("b", 20), freestyleMaxLineRunes)
	if !errors.Is(err, ErrFreestyleLineTooLong) {
		t.Fatalf("got %v, want ErrFreestyleLineTooLong", err)
	}
}

func TestWrapFreestyleSQL_LongStarCommentLineIsCutLocally(t *testing.T) {
	got, err := wrapFreestyleSQL("*"+strings.Repeat(" a", 200), freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(got); n != freestyleMaxLineRunes {
		t.Errorf("comment line should be cut to %d runes, got %d", freestyleMaxLineRunes, n)
	}
}

func TestWrapFreestyleSQL_TrailingBlanksAreDropped(t *testing.T) {
	line := "SELECT * FROM t000 WHERE mandt = '100'"
	for _, blanks := range []string{strings.Repeat(" ", 300), strings.Repeat("\t", 300)} {
		got, err := wrapFreestyleSQL(line+blanks, freestyleMaxLineRunes)
		if err != nil {
			t.Fatal(err)
		}
		if got != line {
			t.Errorf("got %q, want %q", got, line)
		}
	}
}

func TestWrapFreestyleSQL_TrailingCommentMovesWhole(t *testing.T) {
	code := "SELECT * FROM t000 WHERE mandt = '100' AND " + strings.Repeat("b", 150) + " = 1"
	comment := `" ` + strings.Repeat("c", 100)
	got, err := wrapFreestyleSQL(code+" "+comment, freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || lines[0] != code || lines[1] != comment {
		t.Errorf("comment should sit on its own line:\n%s", got)
	}
}

func TestWrapFreestyleSQL_CommentLongerThanTheLimitIsCutLocally(t *testing.T) {
	code := "SELECT * FROM t000"
	got, err := wrapFreestyleSQL(code+" "+`" `+strings.Repeat("c ", 200), freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || lines[0] != code || utf8.RuneCountInString(lines[1]) != freestyleMaxLineRunes {
		t.Errorf("code stays, comment is cut to %d runes:\n%q", freestyleMaxLineRunes, got)
	}
}

func TestWrapFreestyleSQL_ManySegments(t *testing.T) {
	var conds []string
	for i := 0; i < 60; i++ {
		conds = append(conds, "campo"+strings.Repeat("z", 8)+" <> 'X'")
	}
	sql := "SELECT COUNT( * ) FROM t WHERE " + strings.Join(conds, " AND ")
	got, err := wrapFreestyleSQL(sql, freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(got, "\n")); n < 3 {
		t.Errorf("expected 3 or more lines, got %d", n)
	}
	if maxLineRunes(got) > freestyleMaxLineRunes {
		t.Errorf("a line is still too long:\n%s", got)
	}
	if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(sql), " ") {
		t.Error("wrapping changed something other than whitespace")
	}
}

func TestWrapFreestyleSQL_CountsRunesNotBytes(t *testing.T) {
	// 240 runes but 480 bytes: no wrap needed. 260 runes: wrap needed.
	short := "SELECT * FROM t WHERE a = '" + strings.Repeat("ñ", 200) + "' AND b = 1"
	if got, _ := wrapFreestyleSQL(short, freestyleMaxLineRunes); got != short {
		t.Error("a line under the limit in runes must not change even if it is over in bytes")
	}
	long := "SELECT * FROM t WHERE a = '" + strings.Repeat("€", 200) + "' AND b = 1 AND c = 2 AND d = 3 AND e = 4 AND f = 5 AND g = 6"
	got, err := wrapFreestyleSQL(long, freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	if maxLineRunes(got) > freestyleMaxLineRunes {
		t.Errorf("a line is still too long in runes:\n%s", got)
	}
}

func TestWrapFreestyleSQL_CRLFIsKept(t *testing.T) {
	short := "SELECT *\r\nFROM t000\r\n"
	got, err := wrapFreestyleSQL(short+freestyleQuery275, freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, short) {
		t.Error("short CRLF lines must be kept exactly")
	}

	got, err = wrapFreestyleSQL(freestyleQuery275+"\r\nSELECT 1", freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], "\r") || !strings.HasSuffix(lines[1], "\r") {
		t.Errorf("both halves of a wrapped CRLF line must keep the CR: %q", lines)
	}
}

func TestWrapFreestyleSQL_IsIdempotent(t *testing.T) {
	for _, sql := range []string{
		freestyleQuery275,
		strings.Repeat("campo <> 'X' AND ", 40) + "x = 1",
		"SELECT * FROM t WHERE a IN (" + strings.Repeat("'AAAAAA',", 60) + "'Z')",
	} {
		once, err := wrapFreestyleSQL(sql, freestyleMaxLineRunes)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := wrapFreestyleSQL(once, freestyleMaxLineRunes)
		if err != nil {
			t.Fatal(err)
		}
		if once != twice {
			t.Errorf("not idempotent:\n%q\n%q", once, twice)
		}
	}
}

// --- The wire: the body SAP receives must be the wrapped one ---

type freestyleCapture struct {
	mu     sync.Mutex
	bodies []string
}

func (f *freestyleCapture) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-CSRF-Token", "TOKEN")
	if strings.HasSuffix(r.URL.Path, "/datapreview/freestyle") {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

func (f *freestyleCapture) captured() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...)
}

func newFreestyleClient(t *testing.T) (*Client, *freestyleCapture) {
	t.Helper()
	capture := &freestyleCapture{}
	srv := httptest.NewServer(http.HandlerFunc(capture.handler))
	t.Cleanup(srv.Close)
	cfg := NewConfig(srv.URL, "TESTUSER", "secret")
	return NewClientWithTransport(cfg, NewTransport(cfg)), capture
}

func TestRunQuery_SendsTheWrappedBody(t *testing.T) {
	client, capture := newFreestyleClient(t)
	if _, err := client.RunQuery(context.Background(), freestyleQuery275, 10); err != nil {
		t.Fatal(err)
	}
	bodies := capture.captured()
	if len(bodies) != 1 {
		t.Fatalf("got %d freestyle requests, want 1", len(bodies))
	}
	if maxLineRunes(bodies[0]) > freestyleMaxLineRunes {
		t.Errorf("a line of the body SAP received is over the limit:\n%s", bodies[0])
	}
	if strings.Join(strings.Fields(bodies[0]), " ") != strings.Join(strings.Fields(freestyleQuery275), " ") {
		t.Error("the wrapped body differs from the query by more than whitespace")
	}
}

func TestGetTableContents_FilterIsWrappedToo(t *testing.T) {
	client, capture := newFreestyleClient(t)
	filter := strings.Repeat("campo <> 'X' AND ", 30) + "x = 1"
	if _, err := client.GetTableContents(context.Background(), "ztsu_vales", 10, filter); err != nil {
		t.Fatal(err)
	}
	bodies := capture.captured()
	if len(bodies) != 1 || maxLineRunes(bodies[0]) > freestyleMaxLineRunes {
		t.Errorf("filtered table read was not wrapped: %q", bodies)
	}
}

func TestRunQuery_UnwrappableQueryNeverReachesSAP(t *testing.T) {
	client, capture := newFreestyleClient(t)
	_, err := client.RunQuery(context.Background(), "SELECT * FROM t WHERE a = '"+strings.Repeat("x", 300)+"'", 10)
	if !errors.Is(err, ErrFreestyleLineTooLong) {
		t.Fatalf("got %v, want ErrFreestyleLineTooLong", err)
	}
	if n := len(capture.captured()); n != 0 {
		t.Errorf("%d request(s) reached the freestyle endpoint, want 0", n)
	}
}
