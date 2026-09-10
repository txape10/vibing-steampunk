package adt

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const dtelDocFR = `<?xml version="1.0" encoding="UTF-8"?>
<blue:wbobj xmlns:blue="http://www.sap.com/wbobj/dictionary/dtel" xmlns:dtel="http://www.sap.com/dictionary/dtel" xmlns:atom="http://www.w3.org/2005/Atom">
  <atom:link href="versions" rel="http://www.sap.com/adt/relations/versionhistory"/>
  <dtel:dataElement>
    <dtel:typeKind>domain</dtel:typeKind>
    <dtel:typeName>ZDO_DEMO</dtel:typeName>
    <dtel:dataType>CHAR</dtel:dataType>
    <dtel:dataTypeLength>000010</dtel:dataTypeLength>
    <dtel:shortFieldLabel>Court</dtel:shortFieldLabel>
    <dtel:mediumFieldLabel>Moyen</dtel:mediumFieldLabel>
    <dtel:longFieldLabel/>
    <dtel:headingFieldLabel>Entete</dtel:headingFieldLabel>
    <dtel:searchHelp/>
    <dtel:changeDocument>false</dtel:changeDocument>
  </dtel:dataElement>
</blue:wbobj>`

func TestReplaceDataElementLabel(t *testing.T) {
	// Paired element.
	out, err := replaceDataElementLabel([]byte(dtelDocFR), "shortFieldLabel", "Nouveau & <court>")
	if err != nil {
		t.Fatalf("paired: %v", err)
	}
	if !strings.Contains(string(out), "<dtel:shortFieldLabel>Nouveau &amp; &lt;court&gt;</dtel:shortFieldLabel>") {
		t.Errorf("paired replacement not applied: %s", out)
	}
	// Self-closing element gets expanded.
	out, err = replaceDataElementLabel([]byte(dtelDocFR), "longFieldLabel", "Description longue")
	if err != nil {
		t.Fatalf("self-closing: %v", err)
	}
	if !strings.Contains(string(out), "<dtel:longFieldLabel>Description longue</dtel:longFieldLabel>") {
		t.Errorf("self-closing not expanded: %s", out)
	}
	// A "$" in the value must land literally, not as a regexp group ref.
	out, err = replaceDataElementLabel([]byte(dtelDocFR), "mediumFieldLabel", "Prix $1")
	if err != nil {
		t.Fatalf("dollar: %v", err)
	}
	if !strings.Contains(string(out), "<dtel:mediumFieldLabel>Prix $1</dtel:mediumFieldLabel>") {
		t.Errorf("dollar sign mangled: %s", out)
	}
	// Missing element → error.
	if _, err := replaceDataElementLabel([]byte(dtelDocFR), "nonexistentFieldLabel", "x"); err == nil {
		t.Error("expected an error for a missing label element")
	}
}

func TestApplyDataElementLabelPatch_PreservesUnmodelledElements(t *testing.T) {
	s := "Court neuf"
	h := "Entete neuf"
	out, err := applyDataElementLabelPatch([]byte(dtelDocFR), DataElementLabelPatch{Short: &s, Heading: &h})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "<dtel:shortFieldLabel>Court neuf</dtel:shortFieldLabel>") ||
		!strings.Contains(got, "<dtel:headingFieldLabel>Entete neuf</dtel:headingFieldLabel>") {
		t.Errorf("patched labels missing: %s", got)
	}
	// Untouched label and the unmodelled elements survive.
	for _, keep := range []string{
		"<dtel:mediumFieldLabel>Moyen</dtel:mediumFieldLabel>",
		"<dtel:typeName>ZDO_DEMO</dtel:typeName>",
		"<dtel:dataTypeLength>000010</dtel:dataTypeLength>",
		`<atom:link href="versions"`,
		"<dtel:changeDocument>false</dtel:changeDocument>",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q from the document:\n%s", keep, got)
		}
	}
}

// dtelRoute is a stub of the data-element resource for the write path.
type dtelRoute struct {
	putBody  string
	putQuery string
	failPut  bool
	// afterPut is served on GETs that happen after the PUT, for the
	// read-back. Empty means keep serving dtelDocFR.
	afterPut string
	seenPut  bool
}

func (d *dtelRoute) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "/cts/transportchecks"):
		_, _ = io.WriteString(w, `<?xml version="1.0"?><asx:abap xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA><RECORDING></RECORDING><DEVCLASS>$TMP</DEVCLASS></DATA></asx:values></asx:abap>`)
	case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
		w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
		_, _ = io.WriteString(w, testLockXML)
	case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "UNLOCK":
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/activation"):
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/ddic/dataelements/"):
		switch {
		case d.seenPut && d.afterPut != "":
			_, _ = io.WriteString(w, d.afterPut)
		case d.seenPut && !d.failPut && d.putBody != "":
			// SAP would now serve the written state.
			_, _ = io.WriteString(w, d.putBody)
		default:
			_, _ = io.WriteString(w, dtelDocFR)
		}
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/ddic/dataelements/"):
		body, _ := io.ReadAll(r.Body)
		d.putBody = string(body)
		d.putQuery = r.URL.RawQuery
		d.seenPut = true
		if d.failPut {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "boom")
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func TestWriteDataElementLabels_WindowStatefulAndActivated(t *testing.T) {
	rec := &adtRecorder{}
	route := &dtelRoute{}
	client := newStubbedClient(t, rec, route.handler)

	short := "Court neuf"
	if err := client.WriteDataElementLabels(context.Background(), "ZED_DEMO", "FR",
		DataElementLabelPatch{Short: &short}, ""); err != nil {
		t.Fatalf("WriteDataElementLabels: %v", err)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	putAt := indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodPut && strings.Contains(c.path, "/ddic/dataelements/")
	})
	if lockAt < 0 || putAt < 0 || putAt < lockAt {
		t.Fatalf("expected LOCK then PUT; trace:\n%v", calls)
	}
	assertWindowStateful(t, calls, lockAt, putAt)

	// The PUT carried the substituted label and the lock handle.
	if !strings.Contains(route.putBody, "<dtel:shortFieldLabel>Court neuf</dtel:shortFieldLabel>") {
		t.Errorf("PUT body missing patched label: %s", route.putBody)
	}
	if !strings.Contains(route.putBody, "<dtel:typeName>ZDO_DEMO</dtel:typeName>") {
		t.Errorf("PUT body dropped the domain — not a whole-doc RMW: %s", route.putBody)
	}
	if !strings.Contains(route.putQuery, "lockHandle=HANDLE-1") {
		t.Errorf("PUT query missing lock handle: %s", route.putQuery)
	}

	// A separate activation POST after the unlock.
	unlockAt := indexOfCall(calls, isUnlock)
	activateAt := indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodPost && strings.Contains(c.path, "/activation")
	})
	if unlockAt < 0 || activateAt < 0 || activateAt < unlockAt {
		t.Errorf("expected UNLOCK then activation; trace:\n%v", calls)
		dumpCalls(t, calls)
	}

	// The document GET and PUT both carried sap-language=FR (LOCK/UNLOCK
	// ride the session language, which is fine — they are not the write).
	for _, c := range calls {
		if !strings.Contains(c.path, "/ddic/dataelements/") {
			continue
		}
		if c.method != http.MethodGet && c.method != http.MethodPut {
			continue
		}
		if c.query.Get("sap-language") != "FR" {
			t.Errorf("data element %s not in FR: %s", c.method, c)
		}
	}
}

func TestWriteDataElementLabels_MissingElementAbortsBeforeLock(t *testing.T) {
	rec := &adtRecorder{}
	// A document whose dtel:dataElement has no mediumFieldLabel at all.
	route := &dtelRoute{}
	docNoMedium := strings.Replace(dtelDocFR, "    <dtel:mediumFieldLabel>Moyen</dtel:mediumFieldLabel>\n", "", 1)
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/ddic/dataelements/") {
			_, _ = io.WriteString(w, docNoMedium)
			return
		}
		route.handler(w, r)
	})

	medium := "Moyen neuf"
	err := client.WriteDataElementLabels(context.Background(), "ZED_DEMO", "FR",
		DataElementLabelPatch{Medium: &medium}, "")
	if err == nil {
		t.Fatal("expected an error when a requested label element is absent")
	}
	for _, c := range rec.snapshot() {
		if isLock(c) {
			t.Errorf("a LOCK was taken despite the pre-lock validation failing:\n%s", c)
		}
	}
}

func TestWriteDataElementLabels_RefusesOverlongLabelBeforeLock(t *testing.T) {
	rec := &adtRecorder{}
	route := &dtelRoute{}
	client := newStubbedClient(t, rec, route.handler)

	tooLong := "MoreThanTenChars"
	err := client.WriteDataElementLabels(context.Background(), "ZED_DEMO", "FR",
		DataElementLabelPatch{Short: &tooLong}, "")
	if err == nil || !strings.Contains(err.Error(), "DDIC limit is 10") {
		t.Fatalf("expected a length-limit error, got: %v", err)
	}
	for _, c := range rec.snapshot() {
		if isLock(c) {
			t.Errorf("a LOCK was taken despite the length check failing:\n%s", c)
		}
	}
}

func TestWriteDataElementLabels_PutFailureReleasesLock(t *testing.T) {
	rec := &adtRecorder{}
	route := &dtelRoute{failPut: true}
	client := newStubbedClient(t, rec, route.handler)

	short := "Court neuf"
	err := client.WriteDataElementLabels(context.Background(), "ZED_DEMO", "FR",
		DataElementLabelPatch{Short: &short}, "")
	if err == nil {
		t.Fatal("expected the PUT failure to surface")
	}
	calls := rec.snapshot()
	if indexOfCall(calls, isLock) < 0 || indexOfCall(calls, isUnlock) < 0 {
		t.Errorf("a failed PUT must still release the lock; trace:\n%v", calls)
		dumpCalls(t, calls)
	}
}

func TestWriteDataElementLabels_ReadBackDetectsWholeObjectReplacement(t *testing.T) {
	rec := &adtRecorder{}
	// After the PUT the label landed but the element's type moved — the PUT
	// was accepted as a whole-object replacement, not a label edit.
	afterPut := strings.NewReplacer(
		"<dtel:shortFieldLabel>Court</dtel:shortFieldLabel>", "<dtel:shortFieldLabel>Court neuf</dtel:shortFieldLabel>",
		"<dtel:typeName>ZDO_DEMO</dtel:typeName>", "<dtel:typeName>CHAR20</dtel:typeName>",
	).Replace(dtelDocFR)
	route := &dtelRoute{afterPut: afterPut}
	client := newStubbedClient(t, rec, route.handler)

	short := "Court neuf"
	err := client.WriteDataElementLabels(context.Background(), "ZED_DEMO", "FR",
		DataElementLabelPatch{Short: &short}, "")
	if err == nil || !strings.Contains(err.Error(), "whole-object replacement") {
		t.Fatalf("expected a whole-object-replacement error, got: %v", err)
	}
}
