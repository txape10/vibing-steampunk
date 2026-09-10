package adt

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const progDocOld = `<?xml version="1.0" encoding="UTF-8"?>
<program:abapProgram xmlns:program="http://www.sap.com/adt/programs/programs" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:description="Old text" adtcore:descriptionTextLimit="70" adtcore:name="ZDEMO_PROG" adtcore:type="PROG/P" adtcore:masterLanguage="EN">
  <adtcore:packageRef adtcore:name="$TMP"/>
</program:abapProgram>`

// progDocNoDesc is the same document without any description attribute.
var progDocNoDesc = strings.Replace(progDocOld,
	`adtcore:description="Old text" adtcore:descriptionTextLimit="70" `, "", 1)

func TestDescriptionObjectURL(t *testing.T) {
	cases := []struct {
		typ, name, parent, want string
		wantErr                 bool
	}{
		{"", "ZDEMO", "", "/sap/bc/adt/programs/programs/ZDEMO", false},
		{"PROG", "zdemo", "", "/sap/bc/adt/programs/programs/ZDEMO", false},
		{"INCL", "ZDEMO_TOP", "", "/sap/bc/adt/programs/includes/ZDEMO_TOP", false},
		{"CLAS", "ZCL_DEMO", "", "/sap/bc/adt/oo/classes/ZCL_DEMO", false},
		{"INTF", "ZIF_DEMO", "", "/sap/bc/adt/oo/interfaces/ZIF_DEMO", false},
		{"FUGR", "ZFG_DEMO", "", "/sap/bc/adt/functions/groups/ZFG_DEMO", false},
		{"FUNC", "Z_FM", "Z_FG", "/sap/bc/adt/functions/groups/Z_FG/fmodules/Z_FM", false},
		{"TABL", "ZDEMO_T", "", "/sap/bc/adt/ddic/tables/zdemo_t", false},
		{"DDLS", "ZDEMO_CDS", "", "/sap/bc/adt/ddic/ddl/sources/zdemo_cds", false},
		{"FUNC", "Z_FM", "", "", true},
		{"DOMA", "ZDO_DEMO", "", "", true},
		{"PROG", "", "", "", true},
	}
	for _, c := range cases {
		got, err := DescriptionObjectURL(c.typ, c.name, c.parent)
		if c.wantErr {
			if err == nil {
				t.Errorf("DescriptionObjectURL(%q,%q,%q): expected an error", c.typ, c.name, c.parent)
			}
			continue
		}
		if err != nil {
			t.Errorf("DescriptionObjectURL(%q,%q,%q): %v", c.typ, c.name, c.parent, err)
			continue
		}
		if got != c.want {
			t.Errorf("DescriptionObjectURL(%q,%q,%q) = %q, want %q", c.typ, c.name, c.parent, got, c.want)
		}
	}
}

func TestParseDescriptionDoc(t *testing.T) {
	desc, limit := parseDescriptionDoc([]byte(progDocOld))
	if desc != "Old text" {
		t.Errorf("description = %q, want %q", desc, "Old text")
	}
	if limit != 70 {
		t.Errorf("limit = %d, want 70", limit)
	}

	desc, limit = parseDescriptionDoc([]byte(progDocNoDesc))
	if desc != "" || limit != 0 {
		t.Errorf("no-description doc: got (%q, %d), want (\"\", 0)", desc, limit)
	}

	// descriptionTextLimit must not be read as a description.
	only := `<x adtcore:descriptionTextLimit="55" adtcore:name="Y"/>`
	if d, l := parseDescriptionDoc([]byte(only)); d != "" || l != 55 {
		t.Errorf("limit-only doc: got (%q, %d), want (\"\", 55)", d, l)
	}

	// Entities are unescaped.
	esc := `<x adtcore:description="A &amp; B &lt;c&gt;" adtcore:name="Y"/>`
	if d, _ := parseDescriptionDoc([]byte(esc)); d != "A & B <c>" {
		t.Errorf("unescape: got %q, want %q", d, "A & B <c>")
	}
}

func TestReplaceDescriptionAttr(t *testing.T) {
	// Replace an existing value; a "&" and quotes in the text are escaped.
	out, err := replaceDescriptionAttr([]byte(progDocOld), `New "&" text`)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if !strings.Contains(string(out), `adtcore:description="New &quot;&amp;&quot; text"`) {
		t.Errorf("replacement not applied/escaped: %s", out)
	}
	if !strings.Contains(string(out), `adtcore:name="ZDEMO_PROG"`) ||
		!strings.Contains(string(out), `<adtcore:packageRef adtcore:name="$TMP"/>`) {
		t.Errorf("replacement disturbed the rest of the document: %s", out)
	}
	if strings.Contains(string(out), "Old text") {
		t.Errorf("old description still present: %s", out)
	}

	// Insert when the document carries no description at all.
	out, err = replaceDescriptionAttr([]byte(progDocNoDesc), "Fresh")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if !strings.Contains(string(out), `adtcore:name="ZDEMO_PROG" adtcore:description="Fresh"`) {
		t.Errorf("attribute not inserted after adtcore:name: %s", out)
	}

	// Nowhere to attach one → error, no bytes.
	if _, err := replaceDescriptionAttr([]byte(`<root/>`), "x"); err == nil {
		t.Error("expected an error for a document with no name and no description")
	}

	// A child element carrying its own description="" is left alone; only
	// the root's is edited.
	withChild := `<?xml version="1.0"?>
<program:abapProgram xmlns:adtcore="http://www.sap.com/adt/core" adtcore:description="Root" adtcore:name="ZDEMO">
  <adtcore:objectReference adtcore:description="a child" adtcore:name="OTHER"/>
</program:abapProgram>`
	out, err = replaceDescriptionAttr([]byte(withChild), "Changed root")
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	if !strings.Contains(string(out), `adtcore:description="Changed root"`) ||
		!strings.Contains(string(out), `adtcore:description="a child"`) {
		t.Errorf("root not changed or child disturbed: %s", out)
	}
	if d, _ := parseDescriptionDoc([]byte(withChild)); d != "Root" {
		t.Errorf("parseDescriptionDoc read a child's description: %q", d)
	}

	// A ">" inside a root attribute value must not cut the tag short.
	gt := `<x:root xmlns:adtcore="c" adtcore:description="a &gt; b" adtcore:name="Y"><child/></x:root>`
	if d, _ := parseDescriptionDoc([]byte(gt)); d != "a > b" {
		t.Errorf("literal > in attr value broke parsing: %q", d)
	}
	out, err = replaceDescriptionAttr([]byte(gt), "c > d")
	if err != nil {
		t.Fatalf("gt replace: %v", err)
	}
	if !strings.Contains(string(out), `adtcore:description="c &gt; d"`) || !strings.Contains(string(out), "<child/>") {
		t.Errorf("gt replace wrong: %s", out)
	}
}

// descRoute stubs the object-metadata resource for the write path.
type descRoute struct {
	current  string // description the GET serves before the PUT
	afterPut string // full doc to serve on GETs after the PUT (empty = derive)
	failPut  bool
	putBody  string
	putQuery string
	seenPut  bool
}

func (d *descRoute) doc(desc string) string {
	return strings.Replace(progDocOld, `adtcore:description="Old text"`, `adtcore:description="`+desc+`"`, 1)
}

func (d *descRoute) handler(w http.ResponseWriter, r *http.Request) {
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
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/programs/programs/"):
		switch {
		case d.seenPut && d.afterPut != "":
			_, _ = io.WriteString(w, d.afterPut)
		case d.seenPut && !d.failPut && d.putBody != "":
			_, _ = io.WriteString(w, d.putBody)
		default:
			_, _ = io.WriteString(w, d.doc(d.current))
		}
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/programs/programs/"):
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

func TestSetDescription_NoOpWhenUnchanged(t *testing.T) {
	rec := &adtRecorder{}
	route := &descRoute{current: "Same text"}
	client := newStubbedClient(t, rec, route.handler)

	res, err := client.SetDescription(context.Background(), "PROG", "ZDEMO_PROG", "", "Same text", "")
	if err != nil {
		t.Fatalf("SetDescription: %v", err)
	}
	if res.Changed {
		t.Errorf("Changed = true for an unchanged description")
	}
	for _, c := range rec.snapshot() {
		if isLock(c) {
			t.Errorf("a LOCK was taken for a no-op change:\n%s", c)
		}
	}
}

func TestSetDescription_WindowStatefulAndActivated(t *testing.T) {
	rec := &adtRecorder{}
	route := &descRoute{current: "Old text"}
	client := newStubbedClient(t, rec, route.handler)

	res, err := client.SetDescription(context.Background(), "PROG", "ZDEMO_PROG", "", "Brand new text", "")
	if err != nil {
		t.Fatalf("SetDescription: %v", err)
	}
	if !res.Changed || res.Old != "Old text" || res.New != "Brand new text" {
		t.Errorf("unexpected result: %+v", res)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	putAt := indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodPut && strings.Contains(c.path, "/programs/programs/")
	})
	if lockAt < 0 || putAt < 0 || putAt < lockAt {
		t.Fatalf("expected LOCK then PUT; trace:\n%v", calls)
	}
	assertWindowStateful(t, calls, lockAt, putAt)

	if !strings.Contains(route.putBody, `adtcore:description="Brand new text"`) {
		t.Errorf("PUT body missing the new description: %s", route.putBody)
	}
	if !strings.Contains(route.putBody, `<adtcore:packageRef adtcore:name="$TMP"/>`) {
		t.Errorf("PUT body dropped the rest of the document — not a whole-doc RMW: %s", route.putBody)
	}
	if !strings.Contains(route.putQuery, "lockHandle=HANDLE-1") {
		t.Errorf("PUT query missing lock handle: %s", route.putQuery)
	}

	unlockAt := indexOfCall(calls, isUnlock)
	activateAt := indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodPost && strings.Contains(c.path, "/activation")
	})
	if unlockAt < 0 || activateAt < 0 || activateAt < unlockAt {
		t.Errorf("expected UNLOCK then activation; trace:\n%v", calls)
		dumpCalls(t, calls)
	}
}

func TestSetDescription_RefusesOverLimitBeforeLock(t *testing.T) {
	rec := &adtRecorder{}
	route := &descRoute{current: "Old text"} // doc advertises descriptionTextLimit="70"
	client := newStubbedClient(t, rec, route.handler)

	tooLong := strings.Repeat("x", 71)
	_, err := client.SetDescription(context.Background(), "PROG", "ZDEMO_PROG", "", tooLong, "")
	if err == nil || !strings.Contains(err.Error(), "limit for ZDEMO_PROG is 70") {
		t.Fatalf("expected a length-limit error, got: %v", err)
	}
	for _, c := range rec.snapshot() {
		if isLock(c) {
			t.Errorf("a LOCK was taken despite the length check failing:\n%s", c)
		}
	}
}

func TestSetDescription_PutFailureReleasesLock(t *testing.T) {
	rec := &adtRecorder{}
	route := &descRoute{current: "Old text", failPut: true}
	client := newStubbedClient(t, rec, route.handler)

	_, err := client.SetDescription(context.Background(), "PROG", "ZDEMO_PROG", "", "New text", "")
	if err == nil {
		t.Fatal("expected the PUT failure to surface")
	}
	calls := rec.snapshot()
	if indexOfCall(calls, isLock) < 0 || indexOfCall(calls, isUnlock) < 0 {
		t.Errorf("a failed PUT must still release the lock; trace:\n%v", calls)
		dumpCalls(t, calls)
	}
}

func TestSetDescription_ReadBackDetectsSilentNoOp(t *testing.T) {
	rec := &adtRecorder{}
	// The PUT "succeeds" but the object still reads the old description.
	route := &descRoute{current: "Old text", afterPut: progDocOld}
	client := newStubbedClient(t, rec, route.handler)

	_, err := client.SetDescription(context.Background(), "PROG", "ZDEMO_PROG", "", "New text", "")
	if err == nil || !strings.Contains(err.Error(), "did not take effect") {
		t.Fatalf("expected a silent-no-op error, got: %v", err)
	}
}

func TestSetDescription_ReadBackDetectsWholeObjectReplacement(t *testing.T) {
	rec := &adtRecorder{}
	// The description landed, but the object came back as a different kind.
	afterPut := strings.Replace(
		strings.Replace(progDocOld, "Old text", "New text", 1),
		"program:abapProgram", "class:abapClass", 2)
	route := &descRoute{current: "Old text", afterPut: afterPut}
	client := newStubbedClient(t, rec, route.handler)

	_, err := client.SetDescription(context.Background(), "PROG", "ZDEMO_PROG", "", "New text", "")
	if err == nil || !strings.Contains(err.Error(), "whole-object replacement") {
		t.Fatalf("expected a whole-object-replacement error, got: %v", err)
	}
}

func TestSetDescription_InsertsAttributeWhenAbsent(t *testing.T) {
	rec := &adtRecorder{}
	route := &descRoute{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/programs/programs/") && !route.seenPut {
			_, _ = io.WriteString(w, progDocNoDesc)
			return
		}
		route.handler(w, r)
	})

	res, err := client.SetDescription(context.Background(), "PROG", "ZDEMO_PROG", "", "Inserted", "")
	if err != nil {
		t.Fatalf("SetDescription: %v", err)
	}
	if res.Old != "" || !res.Changed {
		t.Errorf("unexpected result for an object with no prior description: %+v", res)
	}
	if !strings.Contains(route.putBody, `adtcore:description="Inserted"`) {
		t.Errorf("PUT body missing the inserted description: %s", route.putBody)
	}
}
