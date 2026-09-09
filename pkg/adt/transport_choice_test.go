package adt

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Ported from upstream oisee/vibing-steampunk PR #203's own test file, with
// no changes needed — parseTransportCheck and resolveWriteTransportFor have
// the same signatures and field names in this fork.

func TestParseTransportCheck(t *testing.T) {
	body := `<?xml version="1.0" encoding="utf-8"?><asx:abap version="1.0" xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA><PGMID>LIMU</PGMID><OBJECT>REPS</OBJECT><OBJECTNAME>ZDEMO_B</OBJECTNAME><OPERATION>I</OPERATION><DEVCLASS>ZDEMO</DEVCLASS><KORRFLAG>X</KORRFLAG><RESULT>S</RESULT><RECORDING>X</RECORDING><EXISTING_REQ_ONLY/><MESSAGES/><REQUESTS><CTS_REQUEST><REQ_HEADER><TRKORR>TR-EXAMPLE</TRKORR><TRFUNCTION>K</TRFUNCTION><TRSTATUS>D</TRSTATUS><AS4USER>TESTUSER</AS4USER><AS4TEXT>feature X</AS4TEXT><CLIENT>001</CLIENT></REQ_HEADER></CTS_REQUEST></REQUESTS><LOCKS/><TADIRDEVC>ZDEMO</TADIRDEVC></DATA></asx:values></asx:abap>`
	tc, err := parseTransportCheck([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if !tc.Recording || tc.Package != "ZDEMO" || tc.ObjectName != "ZDEMO_B" || len(tc.Candidates) != 1 {
		t.Fatalf("check: %+v", tc)
	}
	if c := tc.Candidates[0]; c.Number != "TR-EXAMPLE" || c.Status != "D" || c.Description != "feature X" || c.Owner != "TESTUSER" {
		t.Errorf("candidate: %+v", c)
	}
	local, err := parseTransportCheck([]byte(`<asx:abap xmlns:asx="x"><asx:values><DATA><DEVCLASS>$TMP</DEVCLASS><RECORDING/><REQUESTS/></DATA></asx:values></asx:abap>`))
	if err != nil || local.Recording || len(local.Candidates) != 0 {
		t.Errorf("local: %+v %v", local, err)
	}
}

func TestResolveWriteTransportFor(t *testing.T) {
	c := &Client{config: &Config{Safety: SafetyConfig{AllowTransportableEdits: true}}}
	// Named: as given, no note.
	if tr, note, err := c.resolveWriteTransportFor(&TransportChoice{Transport: "TR-EXAMPLE"}, "TR-NAMED", "", "op"); tr != "TR-NAMED" || note != "" || err != nil {
		t.Errorf("named: %q %q %v", tr, note, err)
	}
	// The lock's own request beats the plan.
	if tr, note, err := c.resolveWriteTransportFor(&TransportChoice{Transport: "TR-PLANNED", Reason: "reused"}, "", "TR-LOCKED", "op"); tr != "TR-LOCKED" || note != "" || err != nil {
		t.Errorf("locked: %q %q %v", tr, note, err)
	}
	// The plan, with its reason.
	if tr, note, err := c.resolveWriteTransportFor(&TransportChoice{Transport: "TR-PLANNED", Reason: "reused it"}, "", "", "op"); tr != "TR-PLANNED" || note != "reused it" || err != nil {
		t.Errorf("planned: %q %q %v", tr, note, err)
	}
	// Nothing chosen: the reason still comes back, no request.
	if tr, note, err := c.resolveWriteTransportFor(&TransportChoice{Reason: "left to SAP"}, "", "", "op"); tr != "" || note != "left to SAP" || err != nil {
		t.Errorf("none: %q %q %v", tr, note, err)
	}
	// A failed creation is the write's error.
	if _, _, err := c.resolveWriteTransportFor(&TransportChoice{Err: errTransportCreate}, "", "", "op"); err == nil {
		t.Error("creation failure swallowed")
	}
	// The policy applies to a planned request as to a named one.
	gated := &Client{config: &Config{Safety: SafetyConfig{}}}
	if _, _, err := gated.resolveWriteTransportFor(&TransportChoice{Transport: "TR-PLANNED"}, "", "", "op"); err == nil {
		t.Error("planned request bypassed the transportable-edit gate")
	}
	// nil plan: the old behaviour.
	if tr, _, err := c.resolveWriteTransportFor(nil, "", "TR-LOCKED", "op"); tr != "TR-LOCKED" || err != nil {
		t.Errorf("nil plan: %q %v", tr, err)
	}
	off := &Client{config: &Config{Safety: SafetyConfig{TransportChoice: "off"}}}
	if off.planTransport(context.Background(), "", "/sap/bc/adt/programs/programs/zdemo", "") != nil {
		t.Error("off still planned")
	}
	if c.planTransport(context.Background(), "TR-NAMED", "/sap/bc/adt/programs/programs/zdemo", "") != nil {
		t.Error("a named request still planned")
	}
}

// --- chooseTransport: the core decision logic, exercised end-to-end against
// a stub ADT server. TestParseTransportCheck/TestResolveWriteTransportFor
// above only cover their own halves (XML parsing, policy application on an
// already-built choice) — this is the function that actually decides which
// request a write lands in. ---

type checkCandidate struct{ Number, User, Text, Status string }

func transportCheckXML(recording bool, pkg, objectName string, candidates ...checkCandidate) string {
	var reqs strings.Builder
	for _, c := range candidates {
		reqs.WriteString(`<CTS_REQUEST><REQ_HEADER><TRKORR>` + c.Number +
			`</TRKORR><TRFUNCTION>K</TRFUNCTION><TRSTATUS>` + c.Status +
			`</TRSTATUS><AS4USER>` + c.User + `</AS4USER><AS4TEXT>` + c.Text +
			`</AS4TEXT></REQ_HEADER></CTS_REQUEST>`)
	}
	rec := ""
	if recording {
		rec = "X"
	}
	return `<?xml version="1.0" encoding="utf-8"?><asx:abap version="1.0" xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA>` +
		`<OBJECT>PROG</OBJECT><OBJECTNAME>` + objectName + `</OBJECTNAME><DEVCLASS>` + pkg +
		`</DEVCLASS><RECORDING>` + rec + `</RECORDING><REQUESTS>` + reqs.String() +
		`</REQUESTS><LOCKS/></DATA></asx:values></asx:abap>`
}

// transportDetailXML is a minimal GetTransport response naming one R3TR
// object, for transportHoldsPackage's TADIR lookup.
func transportDetailXML(number, objName string) string {
	return `<?xml version="1.0" encoding="utf-8"?>` +
		`<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">` +
		`<tm:request tm:number="` + number + `" tm:owner="TESTUSER" tm:desc="d" tm:type="K" tm:status="D">` +
		`<tm:abap_object tm:pgmid="R3TR" tm:type="PROG" tm:name="` + objName + `"/>` +
		`</tm:request></tm:root>`
}

// sqlCountRow is a minimal freestyle-query response for
// "SELECT COUNT(*) AS n FROM tadir ...", matching what transportHoldsPackage
// reads via getString(row, "N").
func sqlCountRow(n string) string {
	return `<?xml version="1.0" encoding="utf-8"?>` +
		`<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">` +
		`<dataPreview:columns><dataPreview:metadata dataPreview:name="N" dataPreview:type="C" dataPreview:length="10"/>` +
		`<dataPreview:dataSet><dataPreview:data>` + n + `</dataPreview:data></dataPreview:dataSet>` +
		`</dataPreview:columns></dataPreview:tableData>`
}

func TestChooseTransport_SingleCandidate(t *testing.T) {
	rec := &adtRecorder{}
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO",
		checkCandidate{"TR-A", "TESTUSER", "feature A", "D"})
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, checkXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	choice, err := client.chooseTransport(context.Background(),
		"/sap/bc/adt/programs/programs/zdemo", "ZDEMO_PKG", "", "")
	if err != nil {
		t.Fatalf("chooseTransport: %v", err)
	}
	if choice.Transport != "TR-A" {
		t.Errorf("Transport = %q, want TR-A (the only open request that fits)", choice.Transport)
	}
	if !strings.Contains(choice.Reason, "only open request") {
		t.Errorf("Reason = %q, want it to mention it is the only fitting request", choice.Reason)
	}
	if choice.Created {
		t.Error("Created = true for a reused request")
	}
}

func TestChooseTransport_MultipleCandidates_PicksTheOneThatHoldsThePackage(t *testing.T) {
	rec := &adtRecorder{}
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO",
		checkCandidate{"TR-A", "TESTUSER", "feature A", "D"},
		checkCandidate{"TR-B", "TESTUSER", "feature B", "D"})
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, checkXML)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/TR-A"):
			_, _ = io.WriteString(w, transportDetailXML("TR-A", "ZOBJ_A"))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/TR-B"):
			_, _ = io.WriteString(w, transportDetailXML("TR-B", "ZOBJ_B"))
		case strings.Contains(r.URL.Path, "datapreview/freestyle"):
			body, _ := io.ReadAll(r.Body)
			// Only TR-B's object is in the package, per the TADIR query
			// transportHoldsPackage builds from each candidate's object list.
			if strings.Contains(string(body), "'ZOBJ_B'") {
				_, _ = io.WriteString(w, sqlCountRow("1"))
			} else {
				_, _ = io.WriteString(w, sqlCountRow("0"))
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithEnableTransports()) // transportHoldsPackage's GetTransport is itself gated on this

	choice, err := client.chooseTransport(context.Background(),
		"/sap/bc/adt/programs/programs/zdemo", "ZDEMO_PKG", "", "")
	if err != nil {
		t.Fatalf("chooseTransport: %v", err)
	}
	if choice.Transport != "TR-B" {
		t.Errorf("Transport = %q, want TR-B (it already holds an object of ZDEMO_PKG), got reason %q",
			choice.Transport, choice.Reason)
	}
	if !strings.Contains(choice.Reason, "already holds objects of") {
		t.Errorf("Reason = %q, want it to explain the package match", choice.Reason)
	}
}

func TestChooseTransport_MultipleCandidates_NoneHoldsPackage_PicksNewest(t *testing.T) {
	rec := &adtRecorder{}
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO",
		checkCandidate{"TR-A", "TESTUSER", "feature A", "D"},
		checkCandidate{"TR-B", "TESTUSER", "feature B", "D"})
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, checkXML)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/TR-A"):
			_, _ = io.WriteString(w, transportDetailXML("TR-A", "ZOBJ_A"))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/TR-B"):
			_, _ = io.WriteString(w, transportDetailXML("TR-B", "ZOBJ_B"))
		case strings.Contains(r.URL.Path, "datapreview/freestyle"):
			// Neither candidate's object is in the package.
			_, _ = io.WriteString(w, sqlCountRow("0"))
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	choice, err := client.chooseTransport(context.Background(),
		"/sap/bc/adt/programs/programs/zdemo", "ZDEMO_PKG", "", "")
	if err != nil {
		t.Fatalf("chooseTransport: %v", err)
	}
	if choice.Transport != "TR-B" {
		t.Errorf("Transport = %q, want TR-B (the lexicographically newest of the two, since "+
			"neither already holds the package), got reason %q", choice.Transport, choice.Reason)
	}
	if !strings.Contains(choice.Reason, "newest") {
		t.Errorf("Reason = %q, want it to say it picked the newest", choice.Reason)
	}
}

func TestChooseTransport_NoCandidates_CreatesRequest(t *testing.T) {
	rec := &adtRecorder{}
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO")
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, checkXML)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cts/transportrequests"):
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>`+
				`<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"><tm:request tm:number="TR-NEW"/></tm:root>`)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithEnableTransports())

	choice, err := client.chooseTransport(context.Background(),
		"/sap/bc/adt/programs/programs/zdemo", "ZDEMO_PKG", "", "")
	if err != nil {
		t.Fatalf("chooseTransport: %v", err)
	}
	if choice.Transport != "TR-NEW" || !choice.Created {
		t.Errorf("choice = %+v, want a created request TR-NEW", choice)
	}
	if !strings.Contains(choice.Reason, "created request") {
		t.Errorf("Reason = %q, want it to say a request was created", choice.Reason)
	}
}

func TestChooseTransport_NoCandidates_TransportsDisabled_LeavesItToSAP(t *testing.T) {
	rec := &adtRecorder{}
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO")
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, checkXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	choice, err := client.chooseTransport(context.Background(),
		"/sap/bc/adt/programs/programs/zdemo", "ZDEMO_PKG", "", "")
	if err != nil {
		t.Fatalf("chooseTransport: %v", err)
	}
	if choice.Transport != "" || choice.Created {
		t.Errorf("choice = %+v, want nothing chosen with transports disabled", choice)
	}
	if !strings.Contains(choice.Reason, "SAP will generate one") {
		t.Errorf("Reason = %q, want it to say the choice is left to SAP", choice.Reason)
	}

	calls := rec.snapshot()
	for _, c := range calls {
		if c.method == http.MethodPost && strings.Contains(c.path, "/cts/transportrequests") {
			t.Errorf("a transport was created despite --enable-transports being off: %v", c)
		}
	}
}
