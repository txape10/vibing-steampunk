package mcp

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

func tadirDP(rows [][]string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">`)
	for i, col := range []string{"DEVCLASS", "DELFLAG"} {
		sb.WriteString(`<dataPreview:columns><dataPreview:metadata dataPreview:name="` + col + `" dataPreview:type="C" dataPreview:length="30"/><dataPreview:dataSet>`)
		for _, r := range rows {
			sb.WriteString(`<dataPreview:data>` + html.EscapeString(r[i]) + `</dataPreview:data>`)
		}
		sb.WriteString(`</dataPreview:dataSet></dataPreview:columns>`)
	}
	return sb.String() + `</dataPreview:tableData>`
}

// newTADIRServer answers the TADIR read with rows (or 500 when fail) and
// counts the TADIR reads.
func newTADIRServer(t *testing.T, rows [][]string, fail bool) (*Server, *int) {
	t.Helper()
	reads := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if strings.HasSuffix(r.URL.Path, "/datapreview/freestyle") {
			_, _ = io.Copy(io.Discard, r.Body)
			reads++
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, tadirDP(rows))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	server := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN",
		Mode: "hyperfocused", AllowedPackages: []string{"Z*", "$TMP"}, AllowTransportableEdits: true})
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	return server, &reads
}

const testRequest = "S4DK000001"

func TestPlanPackageCheck_StaleRowWithoutTransportRefusesBeforeCreate(t *testing.T) {
	s, _ := newTADIRServer(t, [][]string{{"ZABAP01", "X"}}, false)
	pc, refused := s.planPackageCheck(context.Background(), "PROG", "ZX", "ZSU01", "", "create")
	if refused == nil || pc != nil || !refused.IsError {
		t.Fatalf("expected a refusal, got pc=%v refused=%v", pc, refused)
	}
	if !strings.Contains(resultText(refused), "Nothing was created") {
		t.Errorf("message should say nothing was created: %s", resultText(refused))
	}
}

func TestPlanPackageCheck_StaleRowWithTransportProceedsAndRemembersOldPackage(t *testing.T) {
	s, _ := newTADIRServer(t, [][]string{{"ZABAP01", "X"}}, false)
	pc, refused := s.planPackageCheck(context.Background(), "PROG", "zx", "zsu01", testRequest, "")
	if refused != nil || pc == nil || pc.stale != "ZABAP01" || pc.pkg != "ZSU01" || pc.name != "ZX" {
		t.Fatalf("unexpected: pc=%+v refused=%v", pc, refused)
	}
}

func TestPlanPackageCheck_StaleRowInDisallowedPackageRefuses(t *testing.T) {
	s, _ := newTADIRServer(t, [][]string{{"SAP_STD", "X"}}, false)
	if _, refused := s.planPackageCheck(context.Background(), "PROG", "ZX", "ZSU01", testRequest, "create"); refused == nil {
		t.Error("a stale row in a package the whitelist forbids cannot be moved back: refuse")
	}
}

func TestPlanPackageCheck_NoStaleRowProceeds(t *testing.T) {
	for name, rows := range map[string][][]string{
		"no row":            nil,
		"live row":          {{"ZSU01", ""}},
		"deleted, same pkg": {{"ZSU01", "X"}},
	} {
		s, _ := newTADIRServer(t, rows, false)
		pc, refused := s.planPackageCheck(context.Background(), "PROG", "ZX", "ZSU01", "", "create")
		if refused != nil || pc == nil || pc.stale != "" {
			t.Errorf("%s: pc=%+v refused=%v", name, pc, refused)
		}
	}
}

func TestPlanPackageCheck_DoesNotApply(t *testing.T) {
	s, reads := newTADIRServer(t, [][]string{{"ZABAP01", "X"}}, false)
	cases := []struct{ typ, pkg, mode string }{
		{"PROG", "$TMP", "create"}, {"PROG", "", "create"}, {"PROG", "ZSU01", "update"}, {"FUGR", "ZSU01", "create"},
	}
	for _, c := range cases {
		if pc, refused := s.planPackageCheck(context.Background(), c.typ, "ZX", c.pkg, "", c.mode); pc != nil || refused != nil {
			t.Errorf("%+v must not apply: pc=%v refused=%v", c, pc, refused)
		}
	}
	if *reads != 0 {
		t.Errorf("a check that does not apply must read nothing, read %d times", *reads)
	}
}

func TestPlanPackageCheck_TADIRReadFailureIsAWarningNotARefusal(t *testing.T) {
	s, _ := newTADIRServer(t, nil, true)
	pc, refused := s.planPackageCheck(context.Background(), "PROG", "ZX", "ZSU01", "", "create")
	if refused != nil || pc == nil || pc.warning == "" {
		t.Fatalf("D4: expected a warning and no refusal, got pc=%+v refused=%v", pc, refused)
	}
}

func TestVerifyPackageAfterCreate(t *testing.T) {
	pc := &packageCheck{objType: "PROG", name: "ZX", pkg: "ZSU01"}

	s, _ := newTADIRServer(t, [][]string{{"ZSU01", ""}}, false)
	res := &adt.WriteSourceResult{}
	if s.verifyPackageAfterCreate(context.Background(), pc, res) || !strings.HasPrefix(res.PackageCheck, "ok") {
		t.Errorf("right package: %q", res.PackageCheck)
	}

	// Wrong package and no explicit transport: reported, never moved, never deleted.
	s, reads := newTADIRServer(t, [][]string{{"ZABAP01", ""}}, false)
	res = &adt.WriteSourceResult{}
	if !s.verifyPackageAfterCreate(context.Background(), pc, res) {
		t.Error("a wrong package must be reported as a mismatch")
	}
	for _, want := range []string{"MISMATCH", "ZABAP01", "ZSU01", "Nothing was deleted", "no explicit transport"} {
		if !strings.Contains(res.PackageCheck, want) {
			t.Errorf("message lacks %q: %s", want, res.PackageCheck)
		}
	}
	if *reads != 1 {
		t.Errorf("only the TADIR read is expected, got %d reads", *reads)
	}

	// D4: TADIR unreadable after the create -> success with a warning.
	s, _ = newTADIRServer(t, nil, true)
	res = &adt.WriteSourceResult{}
	if s.verifyPackageAfterCreate(context.Background(), pc, res) || !strings.HasPrefix(res.PackageCheck, "not verified") {
		t.Errorf("unreadable TADIR: %q", res.PackageCheck)
	}
}

// moveScript answers the dry run and the real move in turn and records the calls.
type moveScript struct {
	dry, real *adt.MoveObjectResult
	dryErr    error
	calls     []bool
}

func (m *moveScript) fn(_ context.Context, _, _, _, _ string, dryRun bool) (*adt.MoveObjectResult, error) {
	m.calls = append(m.calls, dryRun)
	if dryRun {
		return m.dry, m.dryErr
	}
	return m.real, nil
}

// tadirSequence answers successive TADIR reads: before the move, after it.
func newTADIRSequenceServer(t *testing.T, answers ...[][]string) *Server {
	t.Helper()
	n := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if strings.HasSuffix(r.URL.Path, "/datapreview/freestyle") {
			_, _ = io.Copy(io.Discard, r.Body)
			i := n
			if i >= len(answers) {
				i = len(answers) - 1
			}
			n++
			_, _ = io.WriteString(w, tadirDP(answers[i]))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	s := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN",
		Mode: "hyperfocused", AllowedPackages: []string{"Z*", "$TMP"}, AllowTransportableEdits: true})
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	return s
}

func TestVerifyPackageAfterCreate_AutoMove(t *testing.T) {
	wrong := [][]string{{"ZABAP01", ""}}
	right := [][]string{{"ZSU01", ""}}
	dryOK := &adt.MoveObjectResult{Success: true, Recording: "dry_run"}
	realOK := &adt.MoveObjectResult{Success: true, Recording: "recorded", Transport: testRequest}
	cases := []struct {
		name         string
		script       moveScript
		tadir        [][][]string
		wantMismatch bool
		wantText     string
		wantCalls    []bool
	}{
		{"corrected", moveScript{dry: dryOK, real: realOK}, [][][]string{wrong, right}, false, "corrected", []bool{true, false}},
		{"dry run refused", moveScript{dry: &adt.MoveObjectResult{Success: false, Code: "TRANSPORT_LAYER_CHANGE", Message: "no"}}, [][][]string{wrong, wrong}, true, "TRANSPORT_LAYER_CHANGE", []bool{true}},
		{"dry run error", moveScript{dryErr: io.ErrUnexpectedEOF}, [][][]string{wrong, wrong}, true, "dry run failed", []bool{true}},
		{"backend ignores dry_run", moveScript{dry: &adt.MoveObjectResult{Success: true, Recording: "recorded"}}, [][][]string{wrong, wrong}, true, "did not honour dry_run", []bool{true}},
		{"move without recording", moveScript{dry: dryOK, real: &adt.MoveObjectResult{Success: true}}, [][][]string{wrong, wrong}, true, "without recording", []bool{true, false}},
		{"move refused", moveScript{dry: dryOK, real: &adt.MoveObjectResult{Success: false, Code: "OBJECT_DELETED", Message: "x"}}, [][][]string{wrong, wrong}, true, "OBJECT_DELETED", []bool{true, false}},
		{"move ok but TADIR still wrong", moveScript{dry: dryOK, real: realOK}, [][][]string{wrong, wrong}, true, "still does not show", []bool{true, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTADIRSequenceServer(t, c.tadir...)
			sc := c.script
			s.moveFn = sc.fn
			pc := &packageCheck{objType: "PROG", name: "ZX", pkg: "ZSU01", transport: testRequest}
			res := &adt.WriteSourceResult{}
			if got := s.verifyPackageAfterCreate(context.Background(), pc, res); got != c.wantMismatch {
				t.Errorf("mismatch=%v want %v (%s)", got, c.wantMismatch, res.PackageCheck)
			}
			if !strings.Contains(res.PackageCheck, c.wantText) {
				t.Errorf("PackageCheck lacks %q: %s", c.wantText, res.PackageCheck)
			}
			if len(sc.calls) != len(c.wantCalls) {
				t.Fatalf("move calls %v want %v", sc.calls, c.wantCalls)
			}
			for i := range sc.calls {
				if sc.calls[i] != c.wantCalls[i] {
					t.Errorf("move calls %v want %v", sc.calls, c.wantCalls)
				}
			}
			if c.wantMismatch && !strings.Contains(res.PackageCheck, "Nothing was deleted") {
				t.Errorf("a mismatch must say nothing was deleted: %s", res.PackageCheck)
			}
		})
	}
}

func TestIncompleteCreateNote(t *testing.T) {
	if incompleteCreateNote(nil) != "" || incompleteCreateNote(&packageCheck{}) != "" {
		t.Error("no stale row, no note")
	}
	if n := incompleteCreateNote(&packageCheck{objType: "PROG", name: "ZX", pkg: "ZSU01", stale: "ZABAP01"}); !strings.Contains(n, "ZABAP01") {
		t.Errorf("note should name the stale package: %q", n)
	}
}
