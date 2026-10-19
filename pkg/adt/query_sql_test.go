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
)

// The statements below are ones the data preview refused in real use.
func TestNormalizeOpenSQL(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"alias with a period": {
			"SELECT t.lgnum, t.lgtyp FROM /scwm/t301t AS t WHERE t.lgnum = '2000' AND t.spras = 'D'",
			"SELECT t~lgnum, t~lgtyp FROM /scwm/t301t AS t WHERE t~lgnum = '2000' AND t~spras = 'D'",
		},
		"table name with a period": {
			"SELECT E070.TRKORR, E07T.AS4TEXT FROM E070 LEFT OUTER JOIN E07T ON E070.TRKORR = E07T.TRKORR",
			"SELECT E070~TRKORR, E07T~AS4TEXT FROM E070 LEFT OUTER JOIN E07T ON E070~TRKORR = E07T~TRKORR",
		},
		"mixed period and tilde": {
			"SELECT k.trkorr, e.as4user FROM e071k AS k INNER JOIN e070 AS e ON e~trkorr = k~trkorr",
			"SELECT k~trkorr, e~as4user FROM e071k AS k INNER JOIN e070 AS e ON e~trkorr = k~trkorr",
		},
		"DESC in ORDER BY": {
			"SELECT trkorr FROM e070 WHERE as4user = 'X' ORDER BY as4date DESC, trkorr asc",
			"SELECT trkorr FROM e070 WHERE as4user = 'X' ORDER BY as4date DESCENDING, trkorr ASCENDING",
		},
		"closing period": {
			"SELECT * FROM t000.",
			"SELECT * FROM t000",
		},
		"closing semicolon and blanks": {
			"SELECT * FROM t000 ;  \n",
			"SELECT * FROM t000",
		},
		"namespaced table": {
			"SELECT /ZDEMO/TAB.col FROM /ZDEMO/TAB",
			"SELECT /ZDEMO/TAB~col FROM /ZDEMO/TAB",
		},
		"ORDER BY on one line and DESC on the next": {
			"SELECT a FROM ztab ORDER BY a\nDESC",
			"SELECT a FROM ztab ORDER BY a\nDESCENDING",
		},
		"a DESC after UNION is not a sort direction": {
			"SELECT a FROM ztab ORDER BY a UNION SELECT b AS desc FROM zt2",
			"SELECT a FROM ztab ORDER BY a UNION SELECT b AS desc FROM zt2",
		},
		"a DESC after the closing parenthesis of a subquery is left alone": {
			"SELECT a FROM ztab WHERE a IN ( SELECT b FROM zt2 ORDER BY b DESC ) AND c = desc",
			"SELECT a FROM ztab WHERE a IN ( SELECT b FROM zt2 ORDER BY b DESCENDING ) AND c = desc",
		},
		"CRLF is kept": {
			"SELECT t.a\r\nFROM ztab AS t\r\nORDER BY t.a DESC",
			"SELECT t~a\r\nFROM ztab AS t\r\nORDER BY t~a DESCENDING",
		},
		"multibyte literal does not shift the offsets": {
			"SELECT t.a FROM ztab AS t WHERE t.b = 'ñandú' AND t.c = 1 ORDER BY t.a DESC",
			"SELECT t~a FROM ztab AS t WHERE t~b = 'ñandú' AND t~c = 1 ORDER BY t~a DESCENDING",
		},

		// Nothing to rewrite.
		"literals are left alone": {
			"SELECT a~x FROM tab AS a WHERE a~txt = 'a.b DESC' AND a~y LIKE 'Z.%'",
			"SELECT a~x FROM tab AS a WHERE a~txt = 'a.b DESC' AND a~y LIKE 'Z.%'",
		},
		"a period after an unknown qualifier stays": {
			"SELECT x FROM tab WHERE y = 1.5",
			"SELECT x FROM tab WHERE y = 1.5",
		},
		"DESC before ORDER BY stays": {
			"SELECT desc FROM tab",
			"SELECT desc FROM tab",
		},
		"backtick literal": {
			"SELECT x FROM tab AS t WHERE t~y = `t.col DESC.`",
			"SELECT x FROM tab AS t WHERE t~y = `t.col DESC.`",
		},
		"pipe literal with an escaped pipe": {
			"SELECT x FROM tab AS t WHERE t~y = |a\\|t.col| ORDER BY t~x",
			"SELECT x FROM tab AS t WHERE t~y = |a\\|t.col| ORDER BY t~x",
		},
		"doubled quote inside a literal": {
			"SELECT x FROM tab AS t WHERE t~y = 'it''s t.col' ORDER BY t~x",
			"SELECT x FROM tab AS t WHERE t~y = 'it''s t.col' ORDER BY t~x",
		},
		"comment after code": {
			"SELECT x FROM tab AS t \" t.col DESC.\nWHERE t~y = 1",
			"SELECT x FROM tab AS t \" t.col DESC.\nWHERE t~y = 1",
		},
		"comment line": {
			"* t.col ORDER BY x DESC\nSELECT x FROM tab AS t",
			"* t.col ORDER BY x DESC\nSELECT x FROM tab AS t",
		},
		"closing period inside a final comment stays": {
			"SELECT x FROM tab\n\" the end.",
			"SELECT x FROM tab\n\" the end.",
		},
		"an odd quote in a comment does not unmask the next literal": {
			"SELECT x FROM tab AS t \" don't\nWHERE t~y = 'a.b' AND t~z = 'tab.col'",
			"SELECT x FROM tab AS t \" don't\nWHERE t~y = 'a.b' AND t~z = 'tab.col'",
		},
		"an unterminated literal ends with its line": {
			"SELECT x FROM tab AS t WHERE t~y = 'abc\nORDER BY t.x DESC",
			"SELECT x FROM tab AS t WHERE t~y = 'abc\nORDER BY t~x DESCENDING",
		},
		"already ABAP SQL": {
			"SELECT h~trkorr FROM e070 AS h ORDER BY h~trkorr DESCENDING",
			"SELECT h~trkorr FROM e070 AS h ORDER BY h~trkorr DESCENDING",
		},
	} {
		got, notes := normalizeOpenSQL(tc.in)
		if got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", name, got, tc.want)
		}
		if (got != tc.in) != (len(notes) > 0) {
			t.Errorf("%s: notes %v do not match the rewrite", name, notes)
		}
		if again, n2 := normalizeOpenSQL(got); again != got || len(n2) > 0 {
			t.Errorf("%s: not idempotent: %q -> %q", name, got, again)
		}
	}
}

// DESC/ASC is rewritten only as a direction after a sort expression; right
// after BY or a comma it is the expression -- a column named DESC.
func TestNormalizeOpenSQL_AColumnNamedDesc(t *testing.T) {
	for in, want := range map[string]string{
		"SELECT desc FROM ztab ORDER BY desc":       "SELECT desc FROM ztab ORDER BY desc",
		"SELECT a, desc FROM ztab ORDER BY a, desc": "SELECT a, desc FROM ztab ORDER BY a, desc",
		"SELECT desc FROM ztab ORDER BY desc DESC":  "SELECT desc FROM ztab ORDER BY desc DESCENDING",
		"SELECT a FROM ztab ORDER BY a DESC, b ASC": "SELECT a FROM ztab ORDER BY a DESCENDING, b ASCENDING",
	} {
		if got, _ := normalizeOpenSQL(in); got != want {
			t.Errorf("normalizeOpenSQL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The queries vsp builds itself go through RunQuery too; none may change.
func TestNormalizeOpenSQL_InternalQueriesAreUntouched(t *testing.T) {
	for _, q := range []string{
		"SELECT INCLUDE, TYPE AS OTYPE, NAME FROM CROSS WHERE OTYPE = 'ME' AND NAME LIKE 'ZCL_X%'",
		"SELECT COUNT(*) AS n FROM tadir WHERE devclass = 'ZSU01' AND obj_name IN ('A','B','C')",
		"SELECT pgmid, object, obj_name FROM e071 WHERE trkorr = 'TR-EXAMPLE' ORDER BY PGMID, OBJECT",
		"SELECT MAX( AS4DATE ) AS LAST_DATE FROM e070 WHERE trkorr IN ( SELECT trkorr FROM e071 WHERE obj_name IN ( SELECT obj_name FROM tadir ) )",
		"SELECT * FROM ztca_hardcode ORDER BY field",
	} {
		if got, notes := normalizeOpenSQL(q); got != q || len(notes) > 0 {
			t.Errorf("internal query changed:\n %q\n %q (%v)", q, got, notes)
		}
	}
}

func TestMaskFreestyleSQL_KeepsLengthAndLines(t *testing.T) {
	in := "SELECT 'ñ' \" é\n* línea\n|a\\|b| `c`\r\nFROM t"
	out := maskFreestyleSQL(in)
	if len(out) != len(in) || strings.Count(out, "\n") != strings.Count(in, "\n") {
		t.Fatalf("mask changed the shape: %q -> %q", in, out)
	}
	if strings.ContainsAny(out, "ñé") {
		t.Errorf("literal or comment content survived: %q", out)
	}
}

func TestNormalizeThenWrap_FinalLengthFitsAndIsStable(t *testing.T) {
	pad := strings.Repeat("a", 220)
	in := "SELECT t.a FROM ztab AS t WHERE t.b = '" + pad + "' ORDER BY t.a DESC"
	sent, notes := normalizeOpenSQL(in)
	if len(notes) == 0 {
		t.Fatal("expected a rewrite")
	}
	wrapped, err := wrapFreestyleSQL(sent, freestyleMaxLineRunes)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(wrapped, "\n") {
		if n := len([]rune(line)); n > freestyleMaxLineRunes {
			t.Errorf("line %d has %d runes", i+1, n)
		}
	}
	if !strings.Contains(wrapped, "DESCENDING") {
		t.Errorf("the rewrite is lost: %q", wrapped)
	}
	again, _ := normalizeOpenSQL(wrapped)
	rewrapped, err := wrapFreestyleSQL(again, freestyleMaxLineRunes)
	if err != nil || rewrapped != wrapped {
		t.Errorf("normalize+wrap is not idempotent:\n%q\n%q (%v)", wrapped, rewrapped, err)
	}
}

// --- wire ---

const unknownColumnXML = `<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="http://www.sap.com/adt/wda/dataPreview"/><type id="ExceptionDataPreviewGeneral"/><message lang="EN">Unknown column name &quot;VKBURX&quot;.</message><localizedMessage lang="ES">Unknown column name &quot;VKBURX&quot;.</localizedMessage><properties><entry key="T100KEY-ID">ADT_DATAPREVIEW_MSG</entry><entry key="T100KEY-NO">004</entry></properties></exc:exception>`

// The message and number a Spanish logon gets for a name that does not exist.
const notFoundESXML = `<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionDataPreviewGeneral"/><message lang="EN">No existe EDISEGMENTF</message><localizedMessage lang="ES">No existe EDISEGMENTF</localizedMessage><properties><entry key="T100KEY-ID">ADT_DATAPREVIEW_MSG</entry><entry key="T100KEY-NO">022</entry><entry key="T100KEY-V1">EDISEGMENTF</entry></properties></exc:exception>`

const knvvColumns = `<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">
<dataPreview:columns><dataPreview:metadata dataPreview:name="KUNNR"/><dataPreview:dataSet/></dataPreview:columns>
<dataPreview:columns><dataPreview:metadata dataPreview:name="VKORG"/><dataPreview:dataSet/></dataPreview:columns>
<dataPreview:columns><dataPreview:metadata dataPreview:name="VKBUR"/><dataPreview:dataSet/></dataPreview:columns>
</dataPreview:tableData>`

// queryStub answers the freestyle endpoint with whatever the test decides per
// body and the ddic endpoint with fixed column metadata.
type queryStub struct {
	mu       sync.Mutex
	bodies   []string
	ddicHits int
	freestyle func(body string) (status int, resp string)
}

func (q *queryStub) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-CSRF-Token", "TOKEN")
	switch {
	case strings.HasSuffix(r.URL.Path, "/datapreview/freestyle"):
		raw, _ := io.ReadAll(r.Body)
		q.mu.Lock()
		q.bodies = append(q.bodies, string(raw))
		q.mu.Unlock()
		status, resp := 200, ""
		if q.freestyle != nil {
			status, resp = q.freestyle(string(raw))
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	case strings.HasSuffix(r.URL.Path, "/datapreview/ddic"):
		q.mu.Lock()
		q.ddicHits++
		q.mu.Unlock()
		_, _ = io.WriteString(w, knvvColumns)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func newQueryClient(t *testing.T, stub *queryStub) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	cfg := NewConfig(srv.URL, "TESTUSER", "secret")
	return NewClientWithTransport(cfg, NewTransport(cfg))
}

func TestRunQuery_SendsTheNormalizedBodyAndReportsNotes(t *testing.T) {
	stub := &queryStub{}
	c := newQueryClient(t, stub)
	res, err := c.RunQuery(context.Background(), "SELECT t.mandt FROM t000 AS t ORDER BY t.mandt DESC.", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.bodies) != 1 || stub.bodies[0] != "SELECT t~mandt FROM t000 AS t ORDER BY t~mandt DESCENDING" {
		t.Fatalf("body sent: %q", stub.bodies)
	}
	if len(res.Notes) != 3 {
		t.Errorf("notes: %v", res.Notes)
	}
}

func TestRunQuery_NothingToRewriteHasNoNotes(t *testing.T) {
	stub := &queryStub{}
	c := newQueryClient(t, stub)
	res, err := c.RunQuery(context.Background(), "SELECT mandt FROM t000", 10)
	if err != nil || len(res.Notes) != 0 {
		t.Fatalf("%v %v", res, err)
	}
}

func TestRunQuery_UnknownColumnListsTheRealOnes(t *testing.T) {
	stub := &queryStub{freestyle: func(string) (int, string) { return 400, unknownColumnXML }}
	c := newQueryClient(t, stub)
	_, err := c.RunQuery(context.Background(), "SELECT kunnr, vkburx FROM knvv", 10)
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("got %T %v", err, err)
	}
	if qe.Message != `Unknown column name "VKBURX".` {
		t.Errorf("message %q", qe.Message)
	}
	for _, want := range []string{"KNVV has no column VKBURX", "closest: VKBUR", "KUNNR, VKORG, VKBUR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q missing in %s", want, err)
		}
	}
	var api *APIError
	if !errors.As(err, &api) || api.StatusCode != 400 {
		t.Error("the SAP error is no longer reachable")
	}
}

// A Spanish logon words the message in Spanish; the hint keys on the number.
func TestRunQuery_NameNotFoundInSpanishIsExplained(t *testing.T) {
	stub := &queryStub{freestyle: func(body string) (int, string) {
		switch {
		case strings.Contains(body, "FROM dd02l WHERE tabname = 'EDISEGMENTF'"):
			return 200, "" // no row
		case strings.Contains(body, "LIKE 'EDISEG%'"):
			return 200, ""
		}
		return 400, notFoundESXML
	}}
	c := newQueryClient(t, stub)
	_, err := c.RunQuery(context.Background(), "SELECT * FROM edisegmentf", 10)
	if err == nil || !strings.Contains(err.Error(), "no table or view EDISEGMENTF in the dictionary") {
		t.Fatalf("no hint: %v", err)
	}
	if !strings.Contains(err.Error(), "No existe EDISEGMENTF") {
		t.Errorf("SAP's own message is lost: %v", err)
	}
}

func TestRunQuery_FailedLookupDoesNotHideSAPsMessage(t *testing.T) {
	stub := &queryStub{freestyle: func(string) (int, string) { return 400, notFoundESXML }}
	c := newQueryClient(t, stub)
	_, err := c.RunQuery(context.Background(), "SELECT * FROM edisegmentf", 10)
	if err == nil || !strings.Contains(err.Error(), "No existe EDISEGMENTF") || !strings.Contains(err.Error(), "could not be checked") {
		t.Fatalf("%v", err)
	}
}

func TestExplainQueryError_ClientFieldAndPassThrough(t *testing.T) {
	c := newQueryClient(t, &queryStub{})
	xml := `<exc:exception><message lang="EN">The client field &quot;MANDT&quot; cannot be specified in the WHERE condition.</message></exc:exception>`
	err := c.explainQueryError(context.Background(), "SELECT * FROM tbd05 WHERE mandt = '100'", nil, &APIError{StatusCode: 400, Message: xml})
	if !strings.Contains(err.Error(), "drop the condition on MANDT") {
		t.Errorf("no hint: %v", err)
	}
	lraw := `<exc:exception><message lang="EN">Only the prefixed length field can be used to read from the LRAW field or LCHR field SDATA.</message></exc:exception>`
	err = c.explainQueryError(context.Background(), "SELECT sdata FROM edid4", nil, &APIError{StatusCode: 400, Message: lraw})
	if !strings.Contains(err.Error(), "such as SDATA cannot") {
		t.Errorf("LRAW hint: %v", err)
	}
	other := errors.New("connection refused")
	if got := c.explainQueryError(context.Background(), "SELECT 1", nil, other); got != other {
		t.Errorf("a non-SAP error was changed: %v", got)
	}
	notBadRequest := &APIError{StatusCode: 500, Message: xml}
	if got := c.explainQueryError(context.Background(), "SELECT 1", nil, notBadRequest); got != error(notBadRequest) {
		t.Errorf("a non-400 was changed: %v", got)
	}
}

// In a join with the wrong qualifier, one of the tables does have the column;
// its hint must not claim otherwise.
func TestColumnHint_TableThatHasTheColumn(t *testing.T) {
	cols := []string{"KUNNR", "VKORG", "VKBUR"}
	has := columnHint("KNVV", "VKBUR", cols)
	if strings.Contains(has, "has no column") || !strings.Contains(has, "KNVV does have column VKBUR") {
		t.Errorf("hint for a table with the column: %s", has)
	}
	if missing := columnHint("KNVV", "VKBURX", cols); !strings.HasPrefix(missing, "KNVV has no column VKBURX") {
		t.Errorf("hint for a missing column: %s", missing)
	}
}
