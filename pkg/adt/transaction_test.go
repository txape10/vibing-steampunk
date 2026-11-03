package adt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Answers of the VIT resource for TRANT, as a 7.58 system gives them.
const (
	trantSE38    = `<?xml version="1.0" encoding="utf-8"?><adtcore:mainObject adtcore:responsible="SAP" adtcore:masterLanguage="DE" adtcore:masterSystem="SAP" adtcore:name="SE38" adtcore:type="TRAN/T" adtcore:version="active" adtcore:description="ABAP Editor" adtcore:language="ES" xmlns:adtcore="http://www.sap.com/adt/core"><adtcore:packageRef adtcore:uri="/sap/bc/adt/packages/sedt" adtcore:type="DEVC/K" adtcore:name="SEDT"/></adtcore:mainObject>`
	trantUnknown = `<?xml version="1.0" encoding="utf-8"?><adtcore:mainObject adtcore:name="ZNO_SUCH_TX" adtcore:type="TRAN/T" adtcore:version="active" adtcore:language="ES" xmlns:adtcore="http://www.sap.com/adt/core"/>`
	// 7.40 has no VIT at all.
	vitNoHandler = "No application class found for URI: /sap/bc/adt/vit/wb/object_type/TRANT/object_name/S"
)

var stubFromTable = regexp.MustCompile(`(?i)\bFROM\s+(\w+)`)

type tableAnswer struct {
	columns []string
	rows    [][]string
	status  int
}

// transactionStub answers the VIT resource with a fixed status and body, and
// the free-style data preview per table.
type transactionStub struct {
	mu        sync.Mutex
	vitStatus int
	vitBody   string
	tables    map[string]tableAnswer

	requests  int
	vitPath   string
	vitAccept string
	reads     []string
	bodies    []string
}

func (s *transactionStub) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-CSRF-Token", "TOKEN")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	switch {
	case strings.HasPrefix(r.URL.Path, "/sap/bc/adt/vit/"):
		s.vitPath, s.vitAccept = r.URL.EscapedPath(), r.Header.Get("Accept")
		w.WriteHeader(s.vitStatus)
		_, _ = io.WriteString(w, s.vitBody)
	case strings.HasSuffix(r.URL.Path, "/datapreview/freestyle"):
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		s.bodies = append(s.bodies, body)
		table := strings.ToUpper(stubFromTable.FindStringSubmatch(body)[1])
		s.reads = append(s.reads, table)
		a, ok := s.tables[table]
		if !ok {
			_, _ = io.WriteString(w, dpXML([]string{"X"}, nil))
			return
		}
		if a.status != 0 {
			w.WriteHeader(a.status)
			_, _ = io.WriteString(w, "synthetic data preview failure")
			return
		}
		_, _ = io.WriteString(w, dpXML(a.columns, a.rows))
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (s *transactionStub) client(t *testing.T, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	cfg := NewConfig(srv.URL, "TESTUSER", "secret", opts...)
	return NewClientWithTransport(cfg, NewTransport(cfg))
}

func tstcRow(tcode, program, screen, cinfo string) tableAnswer {
	return tableAnswer{columns: []string{"TCODE", "PGMNA", "DYPNO", "CINFO"}, rows: [][]string{{tcode, program, screen, cinfo}}}
}

func tstcpRow(tcode, param string) tableAnswer {
	return tableAnswer{columns: []string{"TCODE", "PARAM"}, rows: [][]string{{tcode, param}}}
}

func TestGetTransaction(t *testing.T) {
	tests := []struct {
		name      string
		stub      *transactionStub
		tcode     string
		want      Transaction
		wantErr   string
		wantReads string
	}{
		{
			name:      "report transaction, header from VIT",
			stub:      &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{"TSTC": tstcRow("SE38", "RSABAPPROGRAM", "1000", "84")}},
			tcode:     "se38",
			want:      Transaction{Name: "SE38", Description: "ABAP Editor", Program: "RSABAPPROGRAM", Package: "SEDT", Responsible: "SAP", Screen: "1000", Kind: "report", CINFO: "84"},
			wantReads: "TSTC",
		},
		{
			name:      "dialog transaction",
			stub:      &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{"TSTC": tstcRow("SE93", "SAPLSEUK", "0390", "04")}},
			tcode:     "SE93",
			want:      Transaction{Name: "SE93", Description: "ABAP Editor", Program: "SAPLSEUK", Package: "SEDT", Responsible: "SAP", Screen: "0390", Kind: "dialog", CINFO: "04"},
			wantReads: "TSTC",
		},
		{
			name: "parameter transaction without a program",
			stub: &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{
				"TSTC":  tstcRow("/ZDEMO/CODE", "", "0000", "02"),
				"TSTCP": tstcpRow("/ZDEMO/CODE", "/*SM30 VIEWNAME=/ZDEMO/V_CODE;UPDATE=X;"),
			}},
			tcode:     "/ZDEMO/CODE",
			want:      Transaction{Name: "/ZDEMO/CODE", Description: "ABAP Editor", Package: "SEDT", Responsible: "SAP", Kind: "parameter", CINFO: "02", Parameter: "/*SM30 VIEWNAME=/ZDEMO/V_CODE;UPDATE=X;", CalledTransaction: "SM30"},
			wantReads: "TSTC,TSTCP",
		},
		{
			name: "parameter transaction with a program",
			stub: &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{
				"TSTC":  tstcRow("F-01", "SAPMF05A", "0100", "02"),
				"TSTCP": tstcpRow("F-01", "/NFBM1 BKPF-BLART=AB;"),
			}},
			tcode:     "F-01",
			want:      Transaction{Name: "F-01", Description: "ABAP Editor", Program: "SAPMF05A", Package: "SEDT", Responsible: "SAP", Screen: "0100", Kind: "parameter", CINFO: "02", Parameter: "/NFBM1 BKPF-BLART=AB;", CalledTransaction: "FBM1"},
			wantReads: "TSTC,TSTCP",
		},
		{
			name: "variant transaction",
			stub: &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{
				"TSTC":  tstcRow("CO04N", "", "0000", "02"),
				"TSTCP": tstcpRow("CO04N", "@@COHVOMPRINT CO04"),
			}},
			tcode:     "CO04N",
			want:      Transaction{Name: "CO04N", Description: "ABAP Editor", Package: "SEDT", Responsible: "SAP", Kind: "variant", CINFO: "02", Parameter: "@@COHVOMPRINT CO04", Variant: "COHVOMPRINT", CalledTransaction: "CO04"},
			wantReads: "TSTC,TSTCP",
		},
		{
			name: "OO transaction",
			stub: &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{
				"TSTC":  tstcRow("/ZDEMO/COMPARE", "", "0000", "08"),
				"TSTCP": tstcpRow("/ZDEMO/COMPARE", `\CLASS=/ZDEMO/CL_COMPARE\METHOD=START`),
			}},
			tcode:     "/ZDEMO/COMPARE",
			want:      Transaction{Name: "/ZDEMO/COMPARE", Description: "ABAP Editor", Package: "SEDT", Responsible: "SAP", Kind: "oo", CINFO: "08", Parameter: `\CLASS=/ZDEMO/CL_COMPARE\METHOD=START`, Class: "/ZDEMO/CL_COMPARE", Method: "START"},
			wantReads: "TSTC,TSTCP",
		},
		{
			name:      "area menu",
			stub:      &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{"TSTC": tstcRow("AC00", "MENUAC00", "1000", "01")}},
			tcode:     "AC00",
			want:      Transaction{Name: "AC00", Description: "ABAP Editor", Program: "MENUAC00", Package: "SEDT", Responsible: "SAP", Screen: "1000", Kind: "area_menu", CINFO: "01"},
			wantReads: "TSTC",
		},
		{
			name:      "unknown transaction: VIT answers 200 but TSTC has no row",
			stub:      &transactionStub{vitStatus: 200, vitBody: trantUnknown},
			tcode:     "ZNO_SUCH_TX",
			wantErr:   "transaction ZNO_SUCH_TX does not exist",
			wantReads: "TSTC",
		},
		{
			name: "no VIT (7.40): header from TSTCT and TADIR",
			stub: &transactionStub{vitStatus: 404, vitBody: vitNoHandler, tables: map[string]tableAnswer{
				"TSTC":  tstcRow("SE38", "RSABAPPROGRAM", "1000", "84"),
				"TSTCT": {columns: []string{"SPRSL", "TCODE", "TTEXT"}, rows: [][]string{{"D", "SE38", "ABAP Editor (de)"}, {"E", "SE38", "ABAP Editor"}}},
				"TADIR": {columns: []string{"OBJ_NAME", "DEVCLASS", "AUTHOR"}, rows: [][]string{{"SE38", "SEDT", "SAP"}}},
			}},
			tcode:     "SE38",
			want:      Transaction{Name: "SE38", Description: "ABAP Editor", Program: "RSABAPPROGRAM", Package: "SEDT", Responsible: "SAP", Screen: "1000", Kind: "report", CINFO: "84"},
			wantReads: "TSTC,TSTCT,TADIR",
		},
		{
			name: "no VIT, TADIR fails: what could be read, and a note",
			stub: &transactionStub{vitStatus: 404, vitBody: vitNoHandler, tables: map[string]tableAnswer{
				"TSTC":  tstcRow("SE38", "RSABAPPROGRAM", "1000", "84"),
				"TADIR": {status: 500},
			}},
			tcode:     "SE38",
			want:      Transaction{Name: "SE38", Program: "RSABAPPROGRAM", Screen: "1000", Kind: "report", CINFO: "84", Note: "header not read: TADIR: "},
			wantReads: "TSTC,TSTCT,TADIR",
		},
		{
			name:      "TSTC fails: header with a note",
			stub:      &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{"TSTC": {status: 500}}},
			tcode:     "SE38",
			want:      Transaction{Name: "SE38", Description: "ABAP Editor", Package: "SEDT", Responsible: "SAP", Note: "what the transaction starts is not known, TSTC: "},
			wantReads: "TSTC",
		},
		{
			name:      "TSTC fails and no header: error",
			stub:      &transactionStub{vitStatus: 404, vitBody: vitNoHandler, tables: map[string]tableAnswer{"TSTC": {status: 500}}},
			tcode:     "SE38",
			wantErr:   "getting transaction SE38: TSTC:",
			wantReads: "TSTC",
		},
		{
			name:      "TSTC fails and VIT knows nothing: error",
			stub:      &transactionStub{vitStatus: 200, vitBody: trantUnknown, tables: map[string]tableAnswer{"TSTC": {status: 500}}},
			tcode:     "ZNO_SUCH_TX",
			wantErr:   "getting transaction ZNO_SUCH_TX: TSTC:",
			wantReads: "TSTC",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := tt.stub.client(t, WithLanguage("EN"))
			got, err := client.GetTransaction(context.Background(), tt.tcode)
			if reads := strings.Join(tt.stub.reads, ","); reads != tt.wantReads {
				t.Errorf("tables read = %s, want %s", reads, tt.wantReads)
			}
			if tt.stub.vitAccept != "application/vnd.sap.adt.basic.object.properties+xml" {
				t.Errorf("VIT Accept = %q", tt.stub.vitAccept)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			// Notes carry the underlying error; compare only the lead.
			if tt.want.Note != "" {
				if !strings.HasPrefix(got.Note, tt.want.Note) {
					t.Errorf("Note = %q, want prefix %q", got.Note, tt.want.Note)
				}
				got.Note = tt.want.Note
			}
			if *got != tt.want {
				t.Errorf("got  %+v\nwant %+v", *got, tt.want)
			}
		})
	}
}

func TestGetTransaction_Path(t *testing.T) {
	s := &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{"TSTC": tstcRow("/ZDEMO/COMPARE", "", "0000", "08")}}
	if _, err := s.client(t).GetTransaction(context.Background(), "/zdemo/compare"); err != nil {
		t.Fatal(err)
	}
	if want := "/sap/bc/adt/vit/wb/object_type/TRANT/object_name/%2FZDEMO%2FCOMPARE"; s.vitPath != want {
		t.Errorf("path = %s, want %s", s.vitPath, want)
	}
}

// The name goes into SQL that has no bind parameters: anything that is not a
// transaction code is refused before a request leaves.
func TestGetTransaction_RefusesNamesThatAreNotTransactionCodes(t *testing.T) {
	for _, bad := range []string{"", "X' OR TCODE LIKE 'Z", "SE38;DROP", "A B", strings.Repeat("A", 21), "SE\n38"} {
		s := &transactionStub{vitStatus: 200, vitBody: trantSE38}
		_, err := s.client(t).GetTransaction(context.Background(), bad)
		if err == nil || !strings.Contains(err.Error(), "invalid transaction name") {
			t.Errorf("%q: err = %v", bad, err)
		}
		if s.requests != 0 {
			t.Errorf("%q: %d requests left the client", bad, s.requests)
		}
	}
}

func TestGetTransaction_SendsTheValidatedCodeInTheSQL(t *testing.T) {
	s := &transactionStub{vitStatus: 200, vitBody: trantSE38, tables: map[string]tableAnswer{"TSTC": tstcRow("SE38", "RSABAPPROGRAM", "1000", "84")}}
	if _, err := s.client(t).GetTransaction(context.Background(), " se38 "); err != nil {
		t.Fatal(err)
	}
	if len(s.bodies) != 1 || !strings.Contains(s.bodies[0], "TCODE = 'SE38'") {
		t.Errorf("bodies = %q", s.bodies)
	}
}

// With free SQL blocked the table reads cannot run: the VIT header is all
// there is, and without it the call is an error, not a silent empty result.
func TestGetTransaction_BlockedFreeSQL(t *testing.T) {
	s := &transactionStub{vitStatus: 200, vitBody: trantSE38}
	safety := UnrestrictedSafetyConfig()
	safety.BlockFreeSQL = true
	got, err := s.client(t, WithSafety(safety)).GetTransaction(context.Background(), "SE38")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Description != "ABAP Editor" || !strings.Contains(got.Note, "what the transaction starts is not known") {
		t.Errorf("got %+v", got)
	}
	if len(s.reads) != 0 {
		t.Errorf("table reads under BlockFreeSQL: %v", s.reads)
	}
}

func TestIsRouterNotFound(t *testing.T) {
	router := &APIError{StatusCode: 404, Message: "No application class found for URI"}
	resource := &APIError{StatusCode: 404, Message: `<?xml version="1.0"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionResourceNotFound"/></exc:exception>`}
	other := &APIError{StatusCode: 500, Message: "boom"}
	if !IsRouterNotFound(router) {
		t.Error("router 404 not recognised")
	}
	if IsRouterNotFound(resource) {
		t.Error("resource 404 taken for a router 404")
	}
	if IsRouterNotFound(other) || IsRouterNotFound(nil) {
		t.Error("non-404 taken for a router 404")
	}
}

func TestTSTCPIsCacheable(t *testing.T) {
	if !stableQuery("SELECT TCODE, PARAM FROM TSTCP WHERE TCODE = 'SE38'") {
		t.Error("TSTCP should be a stable table")
	}
}
