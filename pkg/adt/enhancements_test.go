package adt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	enhoSearchXHH = `<?xml version="1.0" encoding="UTF-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core"><adtcore:objectReference adtcore:uri="/sap/bc/adt/enhancements/enhoxhh/zdemo_enh" adtcore:type="ENHO/XHH" adtcore:name="ZDEMO_ENH" adtcore:packageName="$ZDEMO" adtcore:description="Demo hook"/></adtcore:objectReferences>`
	enhoSearchXHB = `<?xml version="1.0" encoding="UTF-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core"><adtcore:objectReference adtcore:uri="/sap/bc/adt/enhancements/enhoxhb/zdemo_badi_enh" adtcore:type="ENHO/XHB" adtcore:name="ZDEMO_BADI_ENH" adtcore:packageName="$ZDEMO"/></adtcore:objectReferences>`
	enhoSearchXX  = `<?xml version="1.0" encoding="UTF-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core"><adtcore:objectReference adtcore:uri="/sap/bc/adt/enhancements/enhoxzz/zdemo_other" adtcore:type="ENHO/XZZ" adtcore:name="ZDEMO_OTHER"/></adtcore:objectReferences>`
	enhoXHBDoc    = `<?xml version="1.0" encoding="utf-8"?><enho:objectData adtcore:name="ZDEMO_BADI_ENH" adtcore:description="Demo BAdI" adtcore:responsible="TESTUSER" xmlns:enho="http://www.sap.com/adt/enhancements/enho" xmlns:adtcore="http://www.sap.com/adt/core"><adtcore:packageRef adtcore:name="ZDEMO"/><enho:contentSpecific><enho:badiTechnology><enho:badiImplementations><enho:badiImplementation enho:name="ZDEMO_IMPL" enho:shortText="Demo" enho:default="false" enho:active="%s"><enho:enhancementSpot adtcore:name="ZDEMO_SPOT"/><enho:badiDefinition adtcore:name="ZDEMO_BADI"/><enho:implementingClass adtcore:name="ZCL_DEMO_IMPL"/></enho:badiImplementation></enho:badiImplementations></enho:badiTechnology></enho:contentSpecific></enho:objectData>`
)

type enhoStub struct {
	mu           sync.Mutex
	search       string
	sourceStatus int
	xhbActive    string
	xhbStatus    int
	hookStatus   int
	sqlBodies    []string
	paths        []string
	badiImplRows [][]string
}

func (s *enhoStub) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-CSRF-Token", "TOKEN")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, r.URL.Path)
	switch {
	case strings.Contains(r.URL.Path, "informationsystem/search"):
		_, _ = io.WriteString(w, s.search)
	case strings.HasSuffix(r.URL.Path, "/source/main"):
		if s.sourceStatus != 0 {
			w.WriteHeader(s.sourceStatus)
			_, _ = io.WriteString(w, "synthetic failure")
			return
		}
		_, _ = io.WriteString(w, "ENHANCEMENT 1  .\n  lv_x = 1.\nENDENHANCEMENT.")
	case strings.Contains(r.URL.Path, "/enhancements/enhoxhb/"):
		if s.xhbStatus != 0 {
			w.WriteHeader(s.xhbStatus)
			return
		}
		_, _ = io.WriteString(w, strings.Replace(enhoXHBDoc, "%s", s.xhbActive, 1))
	case strings.HasSuffix(r.URL.Path, "/datapreview/freestyle"):
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		s.sqlBodies = append(s.sqlBodies, body)
		switch strings.ToUpper(stubFromTable.FindStringSubmatch(body)[1]) {
		case "ENHINCINX":
			if s.hookStatus != 0 {
				w.WriteHeader(s.hookStatus)
				return
			}
			_, _ = io.WriteString(w, dpXML([]string{"ENHNAME", "PROGRAMNAME", "ID", "FULL_NAME"},
				[][]string{{"ZDEMO_ENH", "SAPLDEMO", "2 ", `\PR:SAPLDEMO\FO:B\SE:END\EI`}, {"ZDEMO_ENH", "SAPLDEMO", "1 ", `\PR:SAPLDEMO\FO:A\SE:END\EI`}}))
		case "BADI_IMPL":
			_, _ = io.WriteString(w, dpXML([]string{"BADI_NAME", "POS", "ENHNAME", "BADI_IMPL", "CLASS_NAME", "IS_DEFAULT"}, s.badiImplRows))
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *enhoStub) client(t *testing.T, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	cfg := NewConfig(srv.URL, "TESTUSER", "secret", opts...)
	return NewClientWithTransport(cfg, NewTransport(cfg))
}

func (s *enhoStub) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.paths)
}

func TestGetEnhancement_SourcePlugin(t *testing.T) {
	stub := &enhoStub{search: enhoSearchXHH}
	c := stub.client(t)
	enh, err := c.GetEnhancement(context.Background(), "zdemo_enh")
	if err != nil {
		t.Fatal(err)
	}
	if enh.Kind != "XHH" || enh.Package != "$ZDEMO" || !strings.Contains(enh.Source, "ENHANCEMENT 1") {
		t.Fatalf("unexpected enhancement: %+v", enh)
	}
	if len(enh.Hooks) != 2 || enh.Hooks[0].ID != 1 || enh.Hooks[1].ID != 2 {
		t.Fatalf("hooks must come sorted by id: %+v", enh.Hooks)
	}
	text, err := c.getEnhancementText(context.Background(), "ZDEMO_ENH")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "* ENHANCEMENT 1 hooked at ") || !strings.Contains(text, "ENDENHANCEMENT.") {
		t.Fatalf("source text should lead with the hook points: %q", text)
	}
}

func TestGetEnhancementText_HookFailureLeavesANote(t *testing.T) {
	stub := &enhoStub{search: enhoSearchXHH, hookStatus: http.StatusForbidden}
	text, err := stub.client(t).getEnhancementText(context.Background(), "ZDEMO_ENH")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "* NOTE: ") || !strings.Contains(text, "ENDENHANCEMENT.") {
		t.Fatalf("a missing hook list must be visible in the text: %q", text)
	}
}

func TestGetEnhancement_InvalidNameSendsNothing(t *testing.T) {
	for _, name := range []string{"", "ZX' OR '1'='1", "ZX Y", "ZX;DROP", strings.Repeat("A", 31)} {
		stub := &enhoStub{search: enhoSearchXHH}
		if _, err := stub.client(t).GetEnhancement(context.Background(), name); err == nil {
			t.Fatalf("%q should be refused", name)
		}
		if n := stub.requests(); n != 0 {
			t.Fatalf("%q: %d requests sent for a refused name", name, n)
		}
	}
}

func TestGetEnhancement_SourceFailureIsNotSwallowed(t *testing.T) {
	stub := &enhoStub{search: enhoSearchXHH, sourceStatus: http.StatusForbidden}
	_, err := stub.client(t).GetEnhancement(context.Background(), "ZDEMO_ENH")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a 403 on the source must surface, got %v", err)
	}
}

func TestGetEnhancement_NotFound(t *testing.T) {
	stub := &enhoStub{search: `<?xml version="1.0"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core"/>`}
	_, err := stub.client(t).GetEnhancement(context.Background(), "ZNOPE")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
}

func TestGetEnhancement_UnknownKind(t *testing.T) {
	stub := &enhoStub{search: enhoSearchXX}
	_, err := stub.client(t).GetEnhancement(context.Background(), "ZDEMO_OTHER")
	if err == nil || !strings.Contains(err.Error(), "XZZ") {
		t.Fatalf("an unsupported subtype must be named, got %v", err)
	}
}

func TestGetEnhancement_BadiImplementation(t *testing.T) {
	stub := &enhoStub{search: enhoSearchXHB, xhbActive: "true", badiImplRows: [][]string{
		{"ZDEMO_BADI", "2 ", "ZDEMO_BADI_ENH", "ZDEMO_IMPL", "ZCL_DEMO_IMPL", ""},
		{"ZDEMO_BADI", "1 ", "ZDEMO_BADI_ENH_OTHER", "ZDEMO_IMPL", "ZCL_DEMO_OTHER", ""},
	}}
	enh, err := stub.client(t).GetEnhancement(context.Background(), "ZDEMO_BADI_ENH")
	if err != nil {
		t.Fatal(err)
	}
	if enh.Kind != "XHB" || len(enh.Implementations) != 1 {
		t.Fatalf("unexpected: %+v", enh)
	}
	i := enh.Implementations[0]
	if i.Class != "ZCL_DEMO_IMPL" || i.Badi != "ZDEMO_BADI" || i.Spot != "ZDEMO_SPOT" || i.Active != "true" || enh.Package != "ZDEMO" {
		t.Fatalf("unexpected implementation: %+v", i)
	}
	if len(enh.Others) != 1 || enh.Others[0].Enhancement != "ZDEMO_BADI_ENH_OTHER" || enh.Others[0].Active != "true" {
		t.Fatalf("the other implementation of the BAdI must be listed, without itself, with its active flag: %+v", enh.Others)
	}
}

func TestBadiImplementations_RefusesInjectionAndReportsInactive(t *testing.T) {
	stub := &enhoStub{xhbActive: "false", badiImplRows: [][]string{{"ZDEMO_BADI", "1 ", "ZDEMO_BADI_ENH", "ZDEMO_IMPL", "ZCL_DEMO_IMPL", ""}}}
	c := stub.client(t)
	if _, _, err := c.BadiImplementations(context.Background(), "X' OR '1'='1"); err == nil {
		t.Fatal("an injected BAdI name must be refused")
	}
	if n := stub.requests(); n != 0 {
		t.Fatalf("%d requests sent for a refused name", n)
	}
	out, _, err := c.BadiImplementations(context.Background(), "zdemo_badi")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Active != "false" {
		t.Fatalf("an inactive implementation must say so: %+v", out)
	}
	if !strings.Contains(stub.sqlBodies[0], "badi_name = 'ZDEMO_BADI'") {
		t.Fatalf("the query should carry the validated upper-case name: %s", stub.sqlBodies[0])
	}
}

func TestBadiImplementations_ActiveReadCapAndOrder(t *testing.T) {
	var rows [][]string
	for i := 0; i < enhoActiveReadLimit+3; i++ {
		rows = append(rows, []string{"ZDEMO_BADI", strconv.Itoa(i + 1), "ZDEMO_E" + strconv.Itoa(i), "ZDEMO_IMPL", "ZCL_X", ""})
	}
	stub := &enhoStub{xhbActive: "true", badiImplRows: rows}
	out, note, err := stub.client(t).BadiImplementations(context.Background(), "ZDEMO_BADI")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(rows) || !strings.Contains(note, "first 25") {
		t.Fatalf("cap note expected, got %d rows, note %q", len(out), note)
	}
	xhb := 0
	for _, p := range stub.paths {
		if strings.Contains(p, "/enhoxhb/") {
			xhb++
		}
	}
	if xhb != enhoActiveReadLimit {
		t.Fatalf("expected %d active reads, got %d", enhoActiveReadLimit, xhb)
	}
	if !strings.Contains(strings.ToLower(stub.sqlBodies[0]), "order by pos") {
		t.Fatalf("query must order by pos: %s", stub.sqlBodies[0])
	}
}

func TestBadiImplementations_FailedActiveReadIsReported(t *testing.T) {
	stub := &enhoStub{xhbStatus: http.StatusForbidden, badiImplRows: [][]string{
		{"ZDEMO_BADI", "1", "ZDEMO_E1", "ZDEMO_IMPL", "ZCL_X", ""},
		{"ZDEMO_BADI", "2", "ZDEMO_E2", "ZDEMO_IMPL", "ZCL_Y", ""},
		{"ZDEMO_BADI", "3", "ZDEMO_E3", "ZDEMO_IMPL", "ZCL_Z", ""}}}
	out, note, err := stub.client(t).BadiImplementations(context.Background(), "ZDEMO_BADI")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].Active != "" || !strings.Contains(note, "not read") {
		t.Fatalf("a failed read must be reported: %+v / %q", out, note)
	}
	xhb := 0
	for _, p := range stub.paths {
		if strings.Contains(p, "/enhoxhb/") {
			xhb++
		}
	}
	if xhb != 1 {
		t.Fatalf("a 403 must stop further reads, got %d", xhb)
	}
}

func TestBadiImplementations_BlockedFreeSQLIsAnError(t *testing.T) {
	stub := &enhoStub{}
	sc := UnrestrictedSafetyConfig()
	sc.BlockFreeSQL = true
	_, _, err := stub.client(t, WithSafety(sc)).BadiImplementations(context.Background(), "ZDEMO_BADI")
	if err == nil {
		t.Fatal("blocked free SQL must not look like no implementations")
	}
}
