package adt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Layout of the English /formatted document as S4D serves it (checked live):
// a header table of "Label   value" rows above the first chapter, then
// pipe-fenced chapters. Names, programs and numbers are synthetic.
const formattedDumpSample = `----------------------------------------------------------------------------------------------------
Category               ABAP programming error
Runtime Errors         CALL_FUNCTION_CONFLICT_TYPE
Except.                CX_SY_DYN_CALL_ILLEGAL_TYPE
ABAP: Program          ZDEMO_REPORT
Application Component  Not assigned
Date and Time          01.01.2026 10:00:00 (UTC)
----------------------------------------------------------------------------------------------------

----------------------------------------------------------------------------------------------------
|Short Text                                                                                        |
|    Type conflict during a function module call                                                   |
----------------------------------------------------------------------------------------------------

----------------------------------------------------------------------------------------------------
|Information on where terminated                                                                   |
|    The termination occurred in ABAP program or include "ZDEMO_REPORT", in "POST_RECEIPT". The    |
|     main program was "ZDEMO_REPORT".                                                             |
|                                                                                                  |
|    In the source code, the termination point is in line 293 of include "ZDEMO_REPORT".           |
----------------------------------------------------------------------------------------------------

----------------------------------------------------------------------------------------------------
|Source Code Extract                                                                               |
----------------------------------------------------------------------------------------------------
|Line |Code                                                                                        |
----------------------------------------------------------------------------------------------------
|  292|    commit_work( ).                                                                         |
|  293|    CALL FUNCTION 'ENQUEUE_READ'.                                                           |
|  294|  ENDMETHOD.                                                                                |
----------------------------------------------------------------------------------------------------

----------------------------------------------------------------------------------------------------
|Contents of system fields                                                                         |
----------------------------------------------------------------------------------------------------
|Name    |Val.                                                                                     |
----------------------------------------------------------------------------------------------------
|SY-SUBRC|4                                                                                        |
|SY-MSGID|ZDEMO                                                                                    |
----------------------------------------------------------------------------------------------------

----------------------------------------------------------------------------------------------------
|Active Calls/Events                                                                               |
----------------------------------------------------------------------------------------------------
|No.   Ty.          Program                             Include                             Line   |
|      Name                                                                                        |
----------------------------------------------------------------------------------------------------
|    3 METHOD       ZDEMO_REPORT                        ZDEMO_REPORT                          293  |
|      LCL_DEMO=>POST_RECEIPT                                                                      |
|    2 MODULE (PBO) SAPMZDEMO                           ???                                    10  |
|      STATUS_0100                                                                                 |
|    1 EVENT        ZDEMO_REPORT                        ZDEMO_REPORT                          554  |
|      START-OF-SELECTION                                                                          |
----------------------------------------------------------------------------------------------------

----------------------------------------------------------------------------------------------------
|Selected Variables                                                                                |
----------------------------------------------------------------------------------------------------
|Name                                                                                              |
|    Val.                                                                                          |
----------------------------------------------------------------------------------------------------
|No.       3 Ty.          METHOD                                                                   |
|Name  LCL_DEMO=>POST_RECEIPT                                                                      |
----------------------------------------------------------------------------------------------------
|IV_EBELN                                                                                          |
|    4500000001                                                                                    |
|    0000000000                                                                                    |
----------------------------------------------------------------------------------------------------
`

func TestParseDumpDetail_FullDocument(t *testing.T) {
	d := parseDumpDetail(formattedDumpSample)

	if d.ErrorType != "CALL_FUNCTION_CONFLICT_TYPE" || d.Exception != "CX_SY_DYN_CALL_ILLEGAL_TYPE" || d.Program != "ZDEMO_REPORT" {
		t.Errorf("header = %q %q %q", d.ErrorType, d.Exception, d.Program)
	}
	if d.Component != "" {
		t.Errorf("Component = %q, \"Not assigned\" must read as none", d.Component)
	}
	if d.ShortText != "Type conflict during a function module call" {
		t.Errorf("ShortText = %q", d.ShortText)
	}
	// The termination text wraps mid-sentence; procedure, include and line
	// must still come out.
	if d.Line != 293 || d.Include != "ZDEMO_REPORT" || d.Procedure != "POST_RECEIPT" || d.MainProgram != "ZDEMO_REPORT" {
		t.Errorf("termination = %q %d %q %q", d.Include, d.Line, d.Procedure, d.MainProgram)
	}
	var failed []int
	for _, s := range d.Source {
		if s.Failed {
			failed = append(failed, s.Line)
		}
	}
	if len(d.Source) != 3 || len(failed) != 1 || failed[0] != 293 {
		t.Errorf("source = %d lines, failed %v; the header's termination line must flag the failing row", len(d.Source), failed)
	}
	if d.SystemFields["SY-MSGID"] != "ZDEMO" {
		t.Errorf("SY-MSGID = %q", d.SystemFields["SY-MSGID"])
	}
	if !d.recognized() {
		t.Error("recognized() = false for a full document")
	}
}

func TestParseDumpStack_TwoRowFramesAndTwoWordTypes(t *testing.T) {
	frames := parseDumpStack(formattedDumpSample)
	if len(frames) != 3 {
		t.Fatalf("frames = %d, want 3: %+v", len(frames), frames)
	}
	if f := frames[0]; f.Position != 3 || f.Type != "METHOD" || f.Line != 293 || f.Name != "LCL_DEMO=>POST_RECEIPT" {
		t.Errorf("frame 0 = %+v", f)
	}
	if f := frames[1]; f.Type != "MODULE (PBO)" || f.Include != "" || f.Name != "STATUS_0100" {
		t.Errorf("frame 1 = %+v, want a two-word type and no include for ???", f)
	}
}

func TestParseDumpDetail_Variables(t *testing.T) {
	d := parseDumpDetail(formattedDumpSample)
	if len(d.Variables) != 1 {
		t.Fatalf("variables = %+v", d.Variables)
	}
	if v := d.Variables[0]; v.Name != "IV_EBELN" || v.Frame != "LCL_DEMO=>POST_RECEIPT" || v.Value != "4500000001 0000000000" {
		t.Errorf("variable = %+v", v)
	}
}

func TestParseDumpDetail_TerminationVariants(t *testing.T) {
	// SAP's wrapper drops the space in "occurred inABAP program" when the break
	// falls there; the include may be a blank at line 0 for SYSTEM-EXIT.
	doc := "|Information on where terminated                                                                   |\n" +
		"|    The termination occurred inABAP program or include \"ZDEMO_REPORT\", in \"FORM_X\". The      |\n" +
		"|    In the source code, the termination point is in line 0 of include \" \".                    |\n" +
		"----------------------------------------------------------------------------------------------------\n"
	got := parseDumpTermination(doc)
	if got.procedure != "FORM_X" {
		t.Errorf("procedure = %q, want FORM_X despite the missing space", got.procedure)
	}
	if got.line != 0 || got.include != "" {
		t.Errorf("a termination outside ABAP source must leave no position, got %q %d", got.include, got.line)
	}
}

func TestParseDumpDetail_CRLFAndForeignLayout(t *testing.T) {
	crlf := strings.ReplaceAll(formattedDumpSample, "\n", "\r\n")
	d := parseDumpDetail(crlf)
	if d.Line != 293 || d.ShortText == "" || len(d.Source) != 3 || len(d.Stack) != 3 {
		t.Errorf("CRLF document: line %d short %q source %d stack %d", d.Line, d.ShortText, len(d.Source), len(d.Stack))
	}

	// Another language's labels: nothing is recognized, and recognized() must
	// say so instead of passing it off as a dump with nothing in it.
	foreign := strings.NewReplacer("Short Text", "Texto breve", "Runtime Errors", "Err.tmpo.ejec.",
		"ABAP: Program", "Programa ABAP", "Information on where terminated", "Informacion de terminacion",
		"Source Code Extract", "Extracto del codigo", "Contents of system fields", "Campos del sistema",
		"Active Calls/Events", "Llamadas activas", "Selected Variables", "Variables seleccionadas").Replace(formattedDumpSample)
	if parseDumpDetail(foreign).recognized() {
		t.Error("a document in another language was reported as recognized")
	}
}

func TestDumpDetailPath(t *testing.T) {
	for in, want := range map[string]string{
		"/sap/bc/adt/vit/runtime/dumps/20260101000000devhost_DEV_00%20TESTUSER%20001": "/sap/bc/adt/runtime/dump/20260101000000devhost_DEV_00%20TESTUSER%20001",
		"/sap/bc/adt/runtime/dump/ABC": "/sap/bc/adt/runtime/dump/ABC",
		"ABC":                          "/sap/bc/adt/runtime/dump/ABC",
	} {
		if got := dumpDetailPath(in); got != want {
			t.Errorf("dumpDetailPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// dumpStub serves /formatted and the viewer page and records what came in.
type dumpStub struct {
	mu        sync.Mutex
	paths     []string
	languages []string
	formatted int // status of /formatted; 0 = 200 with the sample
}

func (s *dumpStub) client(t *testing.T) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/discovery") || r.Method != http.MethodGet {
			w.Header().Set("X-CSRF-Token", "TOKEN")
			return
		}
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.languages = append(s.languages, r.URL.Query().Get("sap-language"))
		s.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/formatted") {
			if s.formatted != 0 {
				w.WriteHeader(s.formatted)
				return
			}
			_, _ = w.Write([]byte(formattedDumpSample))
			return
		}
		_, _ = w.Write([]byte("<html><head><title>Runtime Error: DEMO</title></head><body>" + strings.Repeat("x", 300*1024) + "</body></html>"))
	}))
	t.Cleanup(srv.Close)
	cfg := NewConfig(srv.URL, "TESTUSER", "secret", WithLanguage("ES"))
	return NewClientWithTransport(cfg, NewTransport(cfg))
}

func (s *dumpStub) viewerFetches() int {
	n := 0
	for _, p := range s.paths {
		if !strings.HasSuffix(p, "/formatted") {
			n++
		}
	}
	return n
}

func TestGetDump_ReadsFormattedInEnglishAndSkipsHTML(t *testing.T) {
	stub := &dumpStub{}
	c := stub.client(t)

	got, err := c.GetDump(context.Background(), "/sap/bc/adt/vit/runtime/dumps/ID1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Detail == nil || got.Detail.Line != 293 || got.Title != "Type conflict during a function module call" || got.Program != "ZDEMO_REPORT" || got.ExceptionType != "CX_SY_DYN_CALL_ILLEGAL_TYPE" {
		t.Fatalf("dump = %+v / %+v", got.RuntimeDump, got.Detail)
	}
	if got.RawHTML != "" || stub.viewerFetches() != 0 {
		t.Errorf("the viewer HTML was fetched or returned by default (fetches %d)", stub.viewerFetches())
	}
	if len(stub.paths) != 1 || stub.paths[0] != "/sap/bc/adt/runtime/dump/ID1/formatted" {
		t.Errorf("paths = %v", stub.paths)
	}
	// The session is ES; the parsers need the English labels.
	if stub.languages[0] != "EN" {
		t.Errorf("sap-language = %q, want EN whatever the session language", stub.languages[0])
	}
}

func TestGetDump_IncludeHTMLIsOptInAndCut(t *testing.T) {
	stub := &dumpStub{}
	got, err := stub.client(t).GetDumpWithOptions(context.Background(), "ID1", DumpReadOptions{IncludeHTML: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.RawHTML == "" || len(got.RawHTML) > dumpHTMLCap {
		t.Errorf("RawHTML length = %d, want 1..%d", len(got.RawHTML), dumpHTMLCap)
	}
}

func TestGetDump_MaxVariablesCutsAndSaysSo(t *testing.T) {
	doc := formattedDumpSample
	// two variables
	stub2 := strings.Replace(doc, "|IV_EBELN", "|IV_FIRST\n|    1\n|IV_EBELN", 1)
	if stub2 == doc {
		t.Fatal("fixture not changed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		_, _ = w.Write([]byte(stub2))
	}))
	defer srv.Close()
	cfg := NewConfig(srv.URL, "TESTUSER", "secret")
	c := NewClientWithTransport(cfg, NewTransport(cfg))

	got, err := c.GetDumpWithOptions(context.Background(), "ID1", DumpReadOptions{MaxVariables: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Detail.Variables) != 1 || !strings.Contains(got.Note, "cut to the first 1 of 2") {
		t.Errorf("variables %d, note %q", len(got.Detail.Variables), got.Note)
	}
}

func TestGetDump_UnavailableFallsBackToTitleWithNote(t *testing.T) {
	stub := &dumpStub{formatted: http.StatusNotFound}
	got, err := stub.client(t).GetDump(context.Background(), "ID1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Detail != nil || got.Title != "Runtime Error: DEMO" || got.Note == "" {
		t.Errorf("fallback = detail %v title %q note %q", got.Detail, got.Title, got.Note)
	}
	if got.RawHTML != "" {
		t.Error("the HTML fetched only for the title was returned")
	}
	_, err = stub.client(t).DumpDetail(context.Background(), "ID1")
	if !errors.Is(err, ErrDumpDetailUnavailable) {
		t.Errorf("err = %v, want ErrDumpDetailUnavailable", err)
	}
}

func TestGetDump_UnrecognizedDocumentSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		_, _ = w.Write([]byte("nothing the parser knows"))
	}))
	defer srv.Close()
	cfg := NewConfig(srv.URL, "TESTUSER", "secret")
	got, err := NewClientWithTransport(cfg, NewTransport(cfg)).GetDump(context.Background(), "ID1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Note, "none of its chapters was recognized") {
		t.Errorf("note = %q", got.Note)
	}
}

func TestGetDump_RefusesIdsThatLeaveTheCollection(t *testing.T) {
	stub := &dumpStub{}
	c := stub.client(t)
	for _, id := range []string{"", "../../system", "ID1?x=y", "ID1#frag", `a\b`} {
		if _, err := c.GetDump(context.Background(), id); err == nil || !strings.Contains(err.Error(), "invalid dump id") {
			t.Errorf("id %q: err = %v, want an invalid dump id refusal", id, err)
		}
	}
	if len(stub.paths) != 0 {
		t.Errorf("a refused id still reached SAP: %v", stub.paths)
	}
}

func TestGetDump_KeepsEscapedBlanksOnTheWire(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if strings.HasSuffix(r.URL.Path, "/formatted") {
			raw = r.URL.EscapedPath()
			_, _ = w.Write([]byte(formattedDumpSample))
		}
	}))
	defer srv.Close()
	cfg := NewConfig(srv.URL, "TESTUSER", "secret")
	c := NewClientWithTransport(cfg, NewTransport(cfg))
	if _, err := c.GetDump(context.Background(), "/sap/bc/adt/vit/runtime/dumps/2026DEV_00%20%20TESTUSER%20001"); err != nil {
		t.Fatal(err)
	}
	if raw != "/sap/bc/adt/runtime/dump/2026DEV_00%20%20TESTUSER%20001/formatted" {
		t.Errorf("escaped path on the wire = %q (blanks must stay %%20, not double-encoded)", raw)
	}
}

func TestParseDumpStack_IgnoresTitleOutsideAChapterAndStopsAtNextChapter(t *testing.T) {
	doc := "|  100|    \" see Active Calls/Events below                                                    |\n" +
		"|Active Calls/Events                                                                               |\n" +
		"|No.   Ty.          Program                             Include                             Line   |\n" +
		"----------------------------------------------------------------------------------------------------\n" +
		"|Selected Variables                                                                                |\n" +
		"|    1 METHOD       ZDEMO                               ZDEMO                                 10  |\n"
	if frames := parseDumpStack(doc); len(frames) != 0 {
		t.Errorf("frames = %+v; an empty stack chapter must not read rows of the next chapter", frames)
	}
}

func TestParseDumpDetail_SourceChangedAfterTheDump(t *testing.T) {
	changed := strings.Replace(formattedDumpSample, "|Source Code Extract   ", "|Source Code Extract (Source code changed)   ", 1)
	if changed == formattedDumpSample {
		t.Fatal("fixture not changed")
	}
	if !parseDumpDetail(changed).SourceChanged {
		t.Error("SourceChanged = false for an extract SAP marks as changed")
	}
	if parseDumpDetail(formattedDumpSample).SourceChanged {
		t.Error("SourceChanged = true for an untouched extract")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		_, _ = w.Write([]byte(changed))
	}))
	defer srv.Close()
	cfg := NewConfig(srv.URL, "TESTUSER", "secret")
	got, err := NewClientWithTransport(cfg, NewTransport(cfg)).GetDump(context.Background(), "ID1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Note, "source changed after the dump") {
		t.Errorf("note = %q, the caller must be told the extract may not match", got.Note)
	}
}
