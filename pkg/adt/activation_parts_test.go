package adt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const silentRefusalXML = `<?xml version="1.0" encoding="utf-8"?><chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"><chkl:properties checkExecuted="false" activationExecuted="false" generationExecuted="false"/></chkl:messages>`

const groupURL = "/sap/bc/adt/functions/groups/zdemo"

func inactiveEntryXML(uri, name, user string) string {
	return fmt.Sprintf(`<ioc:entry><ioc:object ioc:user="%s"><ioc:ref adtcore:uri="%s" adtcore:type="FUGR/I" adtcore:name="%s"/></ioc:object><ioc:transport/></ioc:entry>`, user, uri, name)
}

func inactiveListXML(entries ...string) string {
	return `<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/abapxml/inactiveCtsObjects" xmlns:adtcore="http://www.sap.com/adt/core">` +
		strings.Join(entries, "") + `</ioc:inactiveObjects>`
}

// activationStub answers every activation POST with the silent refusal and the
// inactive list with whatever the test wants (or a 500).
func activationStub(listXML string, listStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sap/bc/adt/activation":
			_, _ = io.WriteString(w, silentRefusalXML)
		case r.URL.Path == "/sap/bc/adt/activation/inactiveobjects":
			if listStatus != 0 {
				w.WriteHeader(listStatus)
				return
			}
			_, _ = io.WriteString(w, listXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
}

func countCalls(calls []wireCall, method, path string) int {
	n := 0
	for _, c := range calls {
		if c.method == method && c.path == path {
			n++
		}
	}
	return n
}

func TestParseActivationResult_SilentChecklistIsARefusal(t *testing.T) {
	res, err := parseActivationResult([]byte(silentRefusalXML))
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || !res.silentRefusal {
		t.Fatalf("a checklist with activationExecuted=false and nothing else must be a refusal: %+v", res)
	}
}

func TestParseActivationResult_ChecklistWithoutPropertiesIsStillSuccess(t *testing.T) {
	res, err := parseActivationResult([]byte(`<chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"/>`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.silentRefusal {
		t.Fatalf("no <properties> element is no signal of refusal: %+v", res)
	}
}

func TestInactivePartsOf(t *testing.T) {
	mk := func(uri string, deleted bool) InactiveObjectRecord {
		return InactiveObjectRecord{Object: &InactiveObject{URI: uri, Name: uri, Deleted: deleted}}
	}
	list := []InactiveObjectRecord{
		mk(groupURL, false),
		mk(groupURL+"/includes/lzdemotop", false),
		mk(groupURL+"/includes/lzdemof01", true),
		mk(groupURL+"x/includes/other", false), // sibling with a common prefix
		{Transport: &InactiveObject{URI: groupURL + "/includes/t"}},
	}
	self, parts := inactivePartsOf(strings.ToUpper(groupURL)+"/source/main", list)
	if !self {
		t.Error("the object itself is in the list")
	}
	if len(parts) != 1 || parts[0].URI != groupURL+"/includes/lzdemotop" {
		t.Fatalf("only the live part strictly below the object counts: %+v", parts)
	}
}

func TestActivate_NothingInactiveIsNothingToActivate(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, activationStub(inactiveListXML(), 0))
	res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || len(res.Messages) != 1 || !strings.Contains(res.Messages[0].ShortText, "Nothing to activate") {
		t.Fatalf("want success with 'Nothing to activate': %+v", res)
	}
	if n := countCalls(rec.snapshot(), http.MethodPost, "/sap/bc/adt/activation"); n != 1 {
		t.Errorf("exactly one activation POST expected, got %d", n)
	}
}

func TestActivate_InactivePartsAreNamedNotActivated(t *testing.T) {
	rec := &adtRecorder{}
	list := inactiveListXML(
		inactiveEntryXML(groupURL+"/includes/lzdemotop", "LZDEMOTOP", "TESTUSER"),
		inactiveEntryXML(groupURL+"/includes/lzdemof01", "LZDEMOF01", "OTHERUSER"),
		inactiveEntryXML("/sap/bc/adt/programs/programs/zunrelated", "ZUNRELATED", "TESTUSER"),
	)
	client := newStubbedClient(t, rec, activationStub(list, 0))
	res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatal("pending parts must not read as success")
	}
	if len(res.Inactive) != 2 {
		t.Fatalf("both parts of the group, none unrelated: %+v", res.Inactive)
	}
	text := strings.Join(activationMessages(res), " ")
	for _, want := range []string{"LZDEMOTOP (TESTUSER)", "LZDEMOF01 (OTHERUSER)", "ACTIVATE_MULTI"} {
		if !strings.Contains(text, want) {
			t.Errorf("message lacks %q: %s", want, text)
		}
	}
	// The golden rule: nothing beyond the one refused request goes out.
	if n := countCalls(rec.snapshot(), http.MethodPost, "/sap/bc/adt/activation"); n != 1 {
		t.Errorf("no second activation may be sent, got %d POSTs", n)
	}
}

func TestActivate_ObjectInactiveButRefusedStaysAFailure(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, activationStub(inactiveListXML(inactiveEntryXML(groupURL, "ZDEMO", "TESTUSER")), 0))
	res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || len(res.Messages) == 0 {
		t.Fatalf("an inactive object that SAP refused is a failure with a reason: %+v", res)
	}
}

func TestActivate_LookupFailureKeepsTheRefusal(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, activationStub("", http.StatusInternalServerError))
	res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatal("without the list there is no basis to call it a success")
	}
	if !strings.Contains(strings.Join(activationMessages(res), " "), "could not be read") {
		t.Errorf("the reason must say why: %+v", res.Messages)
	}
}

func TestActivateMultiple_OneObjectGoesThroughTheVerdict_TwoDoNot(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, activationStub(inactiveListXML(), 0))
	one, err := client.ActivateMultiple(context.Background(), []ObjectRef{{URI: groupURL, Name: "ZDEMO"}})
	if err != nil || !one.Success {
		t.Fatalf("a single object delegates to Activate: %+v %v", one, err)
	}
	listsAfterOne := countCalls(rec.snapshot(), http.MethodGet, "/sap/bc/adt/activation/inactiveobjects")
	if listsAfterOne != 1 {
		t.Fatalf("one list lookup expected, got %d", listsAfterOne)
	}

	two, err := client.ActivateMultiple(context.Background(), []ObjectRef{
		{URI: groupURL, Name: "ZDEMO"}, {URI: groupURL + "/includes/lzdemotop", Name: "LZDEMOTOP"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if two.Success || len(two.Messages) == 0 {
		t.Fatalf("an explicit batch that SAP ignored is a failure with a reason: %+v", two)
	}
	if n := countCalls(rec.snapshot(), http.MethodGet, "/sap/bc/adt/activation/inactiveobjects"); n != listsAfterOne {
		t.Errorf("a batch of two must not consult the list (no recursion): %d lookups", n)
	}
}

func TestActivationMessages_NamesInactiveWhenThereIsNoText(t *testing.T) {
	res := &ActivationResult{Inactive: []InactiveObject{{Name: "LZX", User: "U1"}}}
	if got := strings.Join(activationMessages(res), ";"); got != "still inactive: LZX (U1)" {
		t.Errorf("got %q", got)
	}
}

func TestParseActivationResult_WarningOnlyWithoutPropertiesIsSuccess(t *testing.T) {
	res, err := parseActivationResult([]byte(`<chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"><msg type="W" objDescr="x"><shortText><txt>careful</txt></shortText></msg></chkl:messages>`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || len(res.Messages) != 1 {
		t.Fatalf("without <properties> only E/A/X messages fail: %+v", res)
	}
}

func TestActivatePackage_SilentRefusalIsNotReportedAsActivated(t *testing.T) {
	entry := func(uri, name string) string {
		return fmt.Sprintf(`<ioc:entry><ioc:object ioc:user="TESTUSER"><ioc:ref adtcore:uri="%s" adtcore:type="PROG/P" adtcore:name="%s" adtcore:parentUri="/sap/bc/adt/packages/zdemo"/></ioc:object><ioc:transport/></ioc:entry>`, uri, name)
	}
	list := inactiveListXML(
		entry("/sap/bc/adt/programs/programs/za", "ZA"),
		entry("/sap/bc/adt/programs/programs/zb", "ZB"),
	)
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, activationStub(list, 0))
	res, err := client.ActivatePackage(context.Background(), "ZDEMO", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Activated) != 0 || len(res.Failed) != 2 {
		t.Fatalf("SAP activated nothing: %+v", res)
	}
}
