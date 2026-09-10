package adt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Session-affinity harness (issue #91) ---
//
// The existing helpers in crud_reconcile_test.go answer "was this one request
// stateful?". This bug is about a *window*: every request between the LOCK and
// the write that consumes the handle has to stay on the same ADT session, and
// methodPathMock cannot tell a LOCK POST from an UNLOCK POST because it does
// not look at the query string. So these tests drive a real httptest server and
// record method, path, query and X-sap-adt-sessiontype in request order.

// wireCall is one outbound request as the server saw it.
type wireCall struct {
	method      string
	path        string
	query       url.Values
	sessionType string
}

func (c wireCall) String() string {
	return fmt.Sprintf("%-6s %s?%s sessiontype=%q", c.method, c.path, c.query.Encode(), c.sessionType)
}

// adtRecorder wraps an httptest handler and records what reached it.
type adtRecorder struct {
	mu    sync.Mutex
	calls []wireCall
}

func (r *adtRecorder) handler(route http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.calls = append(r.calls, wireCall{
			method:      req.Method,
			path:        req.URL.Path,
			query:       req.URL.Query(),
			sessionType: req.Header.Get("X-sap-adt-sessiontype"),
		})
		r.mu.Unlock()

		// Every ADT reply carries a token, so the CSRF probe stays out of the
		// trace unless a test deliberately provokes it.
		w.Header().Set("X-CSRF-Token", "TOKEN")
		route(w, req)
	}
}

func (r *adtRecorder) snapshot() []wireCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]wireCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// dump prints the whole trace; every failure in this file wants it.
func dumpCalls(t *testing.T, calls []wireCall) {
	t.Helper()
	for i, c := range calls {
		t.Logf("  [%d] %s", i, c)
	}
}

func indexOfCall(calls []wireCall, pred func(wireCall) bool) int {
	for i, c := range calls {
		if pred(c) {
			return i
		}
	}
	return -1
}

func isLock(c wireCall) bool {
	return c.method == http.MethodPost && c.query.Get("_action") == "LOCK"
}

func isUnlock(c wireCall) bool {
	return c.method == http.MethodPost && c.query.Get("_action") == "UNLOCK"
}

func isSourcePut(c wireCall) bool {
	return c.method == http.MethodPut && strings.HasSuffix(c.path, "/source/main")
}

// assertWindowStateful is the assertion this whole bug reduces to: nothing
// between the LOCK and the request that consumes its handle may leave the
// stateful session.
//
// It is written as `!= "stateful"` rather than `== "stateless"` on purpose —
// a request built without going through the usual header-setting path used
// to send no X-sap-adt-sessiontype at all, which ICM treats as stateless
// just the same.
func assertWindowStateful(t *testing.T, calls []wireCall, from, to int) {
	t.Helper()
	for _, c := range calls[from+1 : to] {
		if c.sessionType != "stateful" {
			t.Errorf("request inside the lock window is not stateful: %s\n"+
				"  it retires the ADT session the lock handle lives in, so the write returns\n"+
				"  423 ExceptionResourceInvalidLockHandle (issue #91)", c)
		}
	}
	if t.Failed() {
		dumpCalls(t, calls)
	}
}

const testLockXML = `<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><IS_LOCAL>X</IS_LOCAL>
<MODIFICATION_SUPPORT>NoModification</MODIFICATION_SUPPORT>
</DATA></asx:values></asx:abap>`

const testEmptyCheckXML = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<chkl:messages xmlns:chkl="http://www.sap.com/adt/checklist"/>`

// searchXMLFor renders the quickSearch answer that resolves one object to one
// package. getObjectPackage matches on the canonicalised URI, so this fixture
// has to name the same object the test is mutating or the gate fails for an
// unrelated reason and the real assertion never runs.
func searchXMLFor(uri, name, pkg string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:objectReference adtcore:uri="%s" adtcore:type="PROG/P" adtcore:name="%s" adtcore:packageName="%s"/>
</adtcore:objectReferences>`, uri, name, pkg)
}

// newStubbedClient wires a client to a recording httptest server.
func newStubbedClient(t *testing.T, rec *adtRecorder, route http.HandlerFunc, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(rec.handler(route))
	t.Cleanup(srv.Close)

	cfg := NewConfig(srv.URL, "TESTUSER", "secret", opts...)
	return NewClientWithTransport(cfg, NewTransport(cfg))
}

// --- The primary regression: the package lookup inside the lock window ---

func TestWriteProgram_NoStatelessRequestBetweenLockAndPut(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor(
				"/sap/bc/adt/programs/programs/zdemo_probe", "ZDEMO_PROBE", "$TMP"))
		case strings.Contains(r.URL.Path, "/checkruns"):
			w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
			_, _ = io.WriteString(w, testEmptyCheckXML)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowedPackages("$TMP"))

	if _, err := client.WriteProgram(context.Background(), "ZDEMO_PROBE",
		"REPORT zdemo_probe.\n", ""); err != nil {
		t.Fatalf("WriteProgram: %v", err)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	putAt := indexOfCall(calls, isSourcePut)
	if lockAt < 0 || putAt < 0 || putAt < lockAt {
		t.Fatalf("expected a LOCK followed by a source PUT; trace:\n%v", calls)
	}
	assertWindowStateful(t, calls, lockAt, putAt)
}

func TestEditSource_NoStatelessRequestBetweenLockAndPut(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_EDIT"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor(
				"/sap/bc/adt/programs/programs/zdemo_edit", "ZDEMO_EDIT", "$TMP"))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/source/main"):
			_, _ = io.WriteString(w, "REPORT zdemo_edit.\nWRITE 'old'.\n")
		case strings.Contains(r.URL.Path, "/checkruns"):
			w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
			_, _ = io.WriteString(w, testEmptyCheckXML)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowedPackages("$TMP"))

	result, err := client.EditSourceWithOptions(context.Background(), objectURL,
		"WRITE 'old'.", "WRITE 'new'.", &EditSourceOptions{SyntaxCheck: false})
	if err != nil {
		t.Fatalf("EditSourceWithOptions: %v", err)
	}
	if !result.Success {
		t.Fatalf("edit did not succeed: %s", result.Message)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	putAt := indexOfCall(calls, isSourcePut)
	if lockAt < 0 || putAt < 0 || putAt < lockAt {
		t.Fatalf("expected a LOCK followed by a source PUT; trace:\n%v", calls)
	}
	assertWindowStateful(t, calls, lockAt, putAt)

	// And the package must have been resolved exactly once, above the lock —
	// the second lookup was pure waste even when it did not kill the session.
	searches := 0
	for _, c := range calls {
		if strings.Contains(c.path, "informationsystem/search") {
			searches++
		}
	}
	if searches != 1 {
		t.Errorf("package resolved %d times, want 1 (once, above the lock)", searches)
		dumpCalls(t, calls)
	}
}

// --- CreateTable: the mutation that was itself stateless ---

func TestCreateTable_SourcePutStaysInTheLockSession(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowedPackages("$TMP"))

	err := client.CreateTable(context.Background(), CreateTableOptions{
		Name:        "ZDEMO_TAB",
		Description: "demo",
		Package:     "$TMP",
		Fields:      []TableField{{Name: "MANDT", Type: "mandt", IsKey: true}},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	calls := rec.snapshot()
	putAt := indexOfCall(calls, isSourcePut)
	if putAt < 0 {
		t.Fatalf("expected a PUT of the table source; trace:\n%v", calls)
	}
	if got := calls[putAt].sessionType; got != "stateful" {
		t.Errorf("table source PUT X-sap-adt-sessiontype = %q, want \"stateful\" — "+
			"it carries the lock handle from the stateful LOCK three lines above it "+
			"and could never match its own lock (issue #91)", got)
		dumpCalls(t, calls)
	}

	lockAt := indexOfCall(calls, isLock)
	if lockAt < 0 || lockAt > putAt {
		t.Fatalf("expected a LOCK before the source PUT; trace:\n%v", calls)
	}
	assertWindowStateful(t, calls, lockAt, putAt)
}

func TestCreateTable_RefusesPackageOutsideAllowlist(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, WithAllowedPackages("$TMP"))

	err := client.CreateTable(context.Background(), CreateTableOptions{
		Name:        "ZDEMO_TAB",
		Description: "demo",
		Package:     "ZDEMO_PROD",
		Fields:      []TableField{{Name: "MANDT", Type: "mandt", IsKey: true}},
	})
	if err == nil {
		t.Fatal("CreateTable created a table in ZDEMO_PROD with SAP_ALLOWED_PACKAGES=$TMP — " +
			"the path only ran the op-type check, so --allowed-packages never applied to it")
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("a blocked CreateTable still talked to SAP:")
		dumpCalls(t, calls)
	}
}

// --- PR #203 port: CreateStructure gained the same gate + Stateful fix
// CreateTable already had, plus the transport-choice plumbing ---

func TestCreateStructure_SourcePutStaysInTheLockSession(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowedPackages("$TMP"))

	err := client.CreateStructure(context.Background(), CreateStructureOptions{
		Name:        "ZDEMO_STRU",
		Description: "demo",
		Package:     "$TMP",
		Fields:      []TableField{{Name: "FIELD1", Type: "CHAR", Length: 10}},
	})
	if err != nil {
		t.Fatalf("CreateStructure: %v", err)
	}

	calls := rec.snapshot()
	putAt := indexOfCall(calls, isSourcePut)
	if putAt < 0 {
		t.Fatalf("expected a PUT of the structure source; trace:\n%v", calls)
	}
	if got := calls[putAt].sessionType; got != "stateful" {
		t.Errorf("structure source PUT X-sap-adt-sessiontype = %q, want \"stateful\" — "+
			"this used to be a hand-rolled PUT with no Stateful field, the same defect "+
			"CreateTable had before it was fixed (issue #91)", got)
		dumpCalls(t, calls)
	}

	lockAt := indexOfCall(calls, isLock)
	if lockAt < 0 || lockAt > putAt {
		t.Fatalf("expected a LOCK before the source PUT; trace:\n%v", calls)
	}
	assertWindowStateful(t, calls, lockAt, putAt)
}

func TestCreateStructure_RefusesPackageOutsideAllowlist(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, WithAllowedPackages("$TMP"))

	err := client.CreateStructure(context.Background(), CreateStructureOptions{
		Name:        "ZDEMO_STRU",
		Description: "demo",
		Package:     "ZDEMO_PROD",
		Fields:      []TableField{{Name: "FIELD1", Type: "CHAR", Length: 10}},
	})
	if err == nil {
		t.Fatal("CreateStructure created a structure in ZDEMO_PROD with SAP_ALLOWED_PACKAGES=$TMP — " +
			"this path only ran the op-type check before, so --allowed-packages never applied to it")
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("a blocked CreateStructure still talked to SAP:")
		dumpCalls(t, calls)
	}
}

// --- PR #203 port: the 4 XML-metadata DDIC creators gained checkMutation
// (they only ran checkSafety before), and their shared writeXMLObject
// helper's PUT gained Stateful — it consumed a stateful LOCK's handle
// without carrying the header, the same defect CreateTable had. CreateDomain
// stands in for CreateDataElement/CreateTableType/CreateLockObject, which
// share the exact same writeXMLObject call. ---

func TestWriteXMLObject_PutStaysInTheLockSession(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowedPackages("$TMP"))

	err := client.CreateDomain(context.Background(), CreateDomainOptions{
		Name:     "ZDEMO_DO",
		Package:  "$TMP",
		DataType: "CHAR",
		Length:   10,
	})
	if err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	calls := rec.snapshot()
	putAt := indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodPut && strings.Contains(c.path, "/ddic/domains/")
	})
	if putAt < 0 {
		t.Fatalf("expected a PUT of the domain metadata; trace:\n%v", calls)
	}
	if got := calls[putAt].sessionType; got != "stateful" {
		t.Errorf("domain metadata PUT X-sap-adt-sessiontype = %q, want \"stateful\" — "+
			"writeXMLObject's PUT carries the lockHandle from a stateful LOCK right above it "+
			"and could never match its own lock without this (issue #91)", got)
		dumpCalls(t, calls)
	}

	lockAt := indexOfCall(calls, isLock)
	if lockAt < 0 || lockAt > putAt {
		t.Fatalf("expected a LOCK before the metadata PUT; trace:\n%v", calls)
	}
	assertWindowStateful(t, calls, lockAt, putAt)
}

func TestCreateDomain_RefusesPackageOutsideAllowlist(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, WithAllowedPackages("$TMP"))

	err := client.CreateDomain(context.Background(), CreateDomainOptions{
		Name:     "ZDEMO_DO",
		Package:  "ZDEMO_PROD",
		DataType: "CHAR",
		Length:   10,
	})
	if err == nil {
		t.Fatal("CreateDomain created a domain in ZDEMO_PROD with SAP_ALLOWED_PACKAGES=$TMP — " +
			"this path only ran the op-type check before, so --allowed-packages never applied to it")
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("a blocked CreateDomain still talked to SAP:")
		dumpCalls(t, calls)
	}
}

// TestCreateMessageClass_RefusesPackageOutsideAllowlist pins the fix to
// CreateMessageClass's own mutation gate: it used to pass ObjectURL for an
// object that does not exist yet, which — with AllowedPackages configured —
// would make checkMutationPackage try to resolve the package via
// SearchObject on a nonexistent object and fail with an unrelated "package
// metadata not found" error instead of a clean policy refusal. Package is
// now passed directly, like every other create path.
func TestCreateMessageClass_RefusesPackageOutsideAllowlist(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, WithAllowedPackages("$TMP"))

	err := client.CreateMessageClass(context.Background(), CreateMessageClassOptions{
		Name:    "ZDEMO_MC",
		Package: "ZDEMO_PROD",
	})
	if err == nil {
		t.Fatal("CreateMessageClass created a message class in ZDEMO_PROD with SAP_ALLOWED_PACKAGES=$TMP")
	}
	if strings.Contains(err.Error(), "package metadata not found") || strings.Contains(err.Error(), "resolving package") {
		t.Errorf("gate failed by trying to resolve a package for a not-yet-created object, not by refusing it cleanly: %v", err)
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("a blocked CreateMessageClass still talked to SAP:")
		dumpCalls(t, calls)
	}
}

// --- The other unconditionally-stateless mutation ---

func TestWriteMessageClassTexts_PutStaysInTheLockSession(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK" {
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	ctx := context.Background()
	lock, err := client.LockObject(ctx, "/sap/bc/adt/messageclass/zdemo_mc", "MODIFY")
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	if err := client.WriteMessageClassTexts(ctx, "ZDEMO_MC", "EN",
		[]MessageClassMessage{{Number: "001", Text: "hello"}}, nil, lock.LockHandle, ""); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}

	calls := rec.snapshot()
	putAt := indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodPut && strings.Contains(c.path, "/messageclass/")
	})
	if putAt < 0 {
		t.Fatalf("expected a PUT of the message class; trace:\n%v", calls)
	}
	if got := calls[putAt].sessionType; got != "stateful" {
		t.Errorf("message class PUT X-sap-adt-sessiontype = %q, want \"stateful\" — "+
			"the lockHandle in its query came from a stateful LOCK (issue #91)", got)
		dumpCalls(t, calls)
	}
}

// --- The one hop no config gates: the CSRF refetch mid-write ---

// TestWriteMessageClassTextsAutoLock_StaysInTheLockSession is the auto-lock
// counterpart of TestWriteMessageClassTexts_PutStaysInTheLockSession (issue
// #162's edit MSAG gap): the package lookup gateAndMark performs happens
// once, before the lock, and everything between LOCK and UNLOCK — including
// the description-echo GET this fix added ahead of the PUT — stays stateful.
func TestWriteMessageClassTextsAutoLock_StaysInTheLockSession(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor(
				"/sap/bc/adt/messageclass/zdemo_mc", "ZDEMO_MC", "$TMP"))
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowedPackages("$TMP"))

	err := client.WriteMessageClassTextsAutoLock(context.Background(), "ZDEMO_MC", "EN",
		[]MessageClassMessage{{Number: "001", Text: "hello"}}, nil, "")
	if err != nil {
		t.Fatalf("WriteMessageClassTextsAutoLock: %v", err)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	if lockAt < 0 {
		t.Fatalf("expected a LOCK; trace:\n%v", calls)
	}

	// The package lookup must happen exactly once, before the lock — not
	// repeated inside the window (that repeat is the #91 defect this whole
	// file exists to catch).
	searchCalls := 0
	for _, c := range calls {
		if strings.Contains(c.path, "informationsystem/search") {
			searchCalls++
		}
	}
	if searchCalls != 1 {
		t.Errorf("expected exactly 1 package lookup, got %d; trace:\n%v", searchCalls, calls)
		dumpCalls(t, calls)
	}
	if searchAt := indexOfCall(calls, func(c wireCall) bool {
		return strings.Contains(c.path, "informationsystem/search")
	}); searchAt >= lockAt {
		t.Errorf("package lookup happened at or after the lock (index %d >= %d) — "+
			"a stateless hop there retires the session the lock handle lives in", searchAt, lockAt)
	}

	unlockAt := indexOfCall(calls, isUnlock)
	if unlockAt < 0 || unlockAt < lockAt {
		t.Fatalf("expected an UNLOCK after the LOCK; trace:\n%v", calls)
	}

	putAt := indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodPut && strings.Contains(c.path, "/messageclass/")
	})
	if putAt < 0 || putAt < lockAt || putAt > unlockAt {
		t.Fatalf("expected the message class PUT between LOCK and UNLOCK; trace:\n%v", calls)
	}
	if got := calls[putAt].sessionType; got != "stateful" {
		t.Errorf("message class PUT X-sap-adt-sessiontype = %q, want \"stateful\"", got)
	}

	assertWindowStateful(t, calls, lockAt, unlockAt)
}

// TestCreateMessageClass_ReleasesLockWhenVerifyFails exercises the branch
// code review flagged as untested: CreateMessageClass's own verify-read-back
// guard (mirroring WriteMessageClassTextsAutoLock's, added for the same
// live-confirmed reason — this PUT has been observed to answer 200 while
// persisting nothing) must release the lock and report the failure, not
// leave the object locked or silently report success.
func TestCreateMessageClass_ReleasesLockWhenVerifyFails(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/messageclass/"):
			// The verify read-back: a message class with no messages at
			// all — simulating the live-confirmed silent no-op where the
			// PUT answers 200 but persists nothing.
			w.Header().Set("Content-Type", "application/vnd.sap.adt.mc.messageclass+xml")
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><messageClass xmlns="`+msagNS+`"/>`)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	err := client.CreateMessageClass(context.Background(), CreateMessageClassOptions{
		Name:     "ZDEMO_MC",
		Package:  "$TMP",
		Language: "EN",
		Messages: []MessageClassMessage{{Number: "001", Text: "hello"}},
	})
	if err == nil {
		t.Fatal("expected CreateMessageClass to fail when the initial-messages write does not verify")
	}
	if !strings.Contains(err.Error(), "does not have the expected text") {
		t.Errorf("expected a verify-failure error, got: %v", err)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	if lockAt < 0 {
		t.Fatalf("expected a LOCK for the initial-messages write; trace:\n%v", calls)
	}
	unlockAt := indexOfCall(calls, isUnlock)
	if unlockAt < 0 || unlockAt < lockAt {
		t.Errorf("expected the lock to be released after the verify failure; trace:\n%v", calls)
		dumpCalls(t, calls)
	}

	activateAt := indexOfCall(calls, func(c wireCall) bool {
		return strings.Contains(c.path, "/activation")
	})
	if activateAt >= 0 {
		t.Errorf("did not expect Activate to run after a verify failure; trace:\n%v", calls)
	}
}

func TestCSRFRefetchDuringStatefulWriteStaysStateful(t *testing.T) {
	rec := &adtRecorder{}
	var mu sync.Mutex
	putSeen := 0

	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if isSourcePut(wireCall{method: r.Method, path: r.URL.Path}) {
			mu.Lock()
			putSeen++
			first := putSeen == 1
			mu.Unlock()
			if first {
				// SAP's stock "your token went stale" answer, which sends the
				// transport back to /core/discovery mid-window.
				w.Header().Set("X-CSRF-Token", "Required")
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := client.UpdateSource(context.Background(),
		"/sap/bc/adt/programs/programs/ZDEMO_CSRF/source/main",
		"REPORT zdemo_csrf.\n", "HANDLE-1", ""); err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}

	calls := rec.snapshot()
	probes := 0
	for _, c := range calls {
		if !strings.Contains(c.path, "/core/discovery") {
			continue
		}
		probes++
		if c.sessionType != "stateful" {
			t.Errorf("CSRF probe issued for a stateful write is not stateful: %s\n"+
				"  it lands between the failed write and its retry, and the retry then\n"+
				"  presents a handle whose session the probe just retired (issue #91)", c)
		}
	}
	if probes == 0 {
		t.Fatalf("expected a CSRF probe on /core/discovery; trace:\n%v", calls)
	}
	if t.Failed() {
		dumpCalls(t, calls)
	}
}

// --- The marker must not become a policy hole ---

func markerTestClient(t *testing.T, safety SafetyConfig) *Client {
	t.Helper()
	cfg := NewConfig("https://sap.invalid", "TESTUSER", "secret", WithSafety(safety))
	return NewClientWithTransport(cfg, NewTransport(cfg))
}

func TestMutationMarker_StillEnforcesOperationPolicy(t *testing.T) {
	// The outer workflow passes OpWorkflow ('W'); the inner mutator passes
	// OpUpdate ('U'). With 'U' disallowed the inner call must still be
	// refused — this is precisely what the old all-or-nothing
	// mutationGateSkipKey early return would have let through.
	client := markerTestClient(t, SafetyConfig{
		AllowedPackages: []string{"$TMP"},
		DisallowedOps:   "U",
	})

	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_MARK"
	ctx := withMutationPackageChecked(context.Background(), objectURL)

	err := client.checkMutation(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "UpdateSource",
		ObjectURL: objectURL + "/source/main",
	})
	if err == nil {
		t.Fatal("a marked context skipped the operation-type check: --disallowed-ops U " +
			"no longer blocks UpdateSource once an outer OpWorkflow gate has run")
	}
	if !strings.Contains(err.Error(), "blocked by safety configuration") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMutationMarker_StillEnforcesTransportPolicy(t *testing.T) {
	client := markerTestClient(t, SafetyConfig{
		AllowedPackages: []string{"$TMP"},
		// AllowTransportableEdits stays false: a transport-bound edit is refused.
	})

	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_MARK"
	ctx := withMutationPackageChecked(context.Background(), objectURL)

	err := client.checkMutation(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "UpdateSource",
		ObjectURL: objectURL + "/source/main",
		Transport: "TR-EXAMPLE",
	})
	if err == nil {
		t.Fatal("a marked context skipped the transportable-edit check: " +
			"--allow-transportable-edits is no longer required once an outer gate has run")
	}
	if !strings.Contains(err.Error(), "transportable objects is disabled") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMutationMarker_IsPerObject(t *testing.T) {
	// Marking ZDEMO_MARKED must do nothing for ZDEMO_OTHER, which lives in a
	// package the whitelist does not allow. A blanket "the gate ran" flag would
	// let a workflow that checked one object delegate an unchecked mutation of
	// another.
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "informationsystem/search") {
			_, _ = io.WriteString(w, searchXMLFor(
				"/sap/bc/adt/programs/programs/zdemo_other", "ZDEMO_OTHER", "ZDEMO_PROD"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}, WithAllowedPackages("$TMP"))

	ctx := withMutationPackageChecked(context.Background(),
		"/sap/bc/adt/programs/programs/ZDEMO_MARKED")

	err := client.checkMutation(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "UpdateSource",
		ObjectURL: "/sap/bc/adt/programs/programs/ZDEMO_OTHER/source/main",
	})
	if err == nil {
		t.Fatal("the mark for ZDEMO_MARKED suppressed the package check for ZDEMO_OTHER, " +
			"which lives in ZDEMO_PROD — the marker must be per-object")
	}

	// It must have actually looked ZDEMO_OTHER up rather than guessing.
	if idx := indexOfCall(rec.snapshot(), func(c wireCall) bool {
		return strings.Contains(c.path, "informationsystem/search")
	}); idx < 0 {
		t.Error("no package lookup was performed for the unmarked object")
	}
}

func TestMutationMarker_SkipsOnlyTheLookupForTheMarkedObject(t *testing.T) {
	// The marked object must be accepted without any HTTP traffic at all —
	// that absence of a request is the whole fix.
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request for a marked object: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusOK)
	}, WithAllowedPackages("$TMP"))

	const objectURL = "/sap/bc/adt/oo/classes/ZCL_DEMO_MARK"
	ctx := withMutationPackageChecked(context.Background(), objectURL)

	// /source/main and /includes/testclasses both resolve their package from
	// the parent class, so both are covered by the class's mark.
	for _, target := range []string{
		objectURL,
		objectURL + "/source/main",
		objectURL + "/includes/testclasses",
	} {
		if err := client.checkMutation(ctx, MutationContext{
			Op:        OpUpdate,
			OpName:    "UpdateSource",
			ObjectURL: target,
		}); err != nil {
			t.Errorf("marked object %s was rejected: %v", target, err)
		}
	}
}

// --- The stranded-ENQUEUE half ---

func TestRenameObject_ReleasesOldLockWhenDeleteFails(t *testing.T) {
	const oldURL = "/sap/bc/adt/programs/programs/ZDEMO_OLD"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor(
				"/sap/bc/adt/programs/programs/zdemo_old", "ZDEMO_OLD", "$TMP"))
		case strings.Contains(r.URL.Path, "nodestructure"):
			// packageExists preflight for CreateObject.
			_, _ = io.WriteString(w, `<?xml version="1.0"?><asx:abap xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA/></asx:values></asx:abap>`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/source/main"):
			_, _ = io.WriteString(w, "REPORT zdemo_old.\n")
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		case r.Method == http.MethodDelete:
			// The failure this test exists for: the old object cannot be
			// deleted, and until now the lock taken to delete it was simply
			// abandoned — on the very object the user is then told to delete
			// by hand.
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, "object is in use")
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowedPackages("$TMP"))

	result, err := client.RenameObject(context.Background(), ObjectTypeProgram,
		"ZDEMO_OLD", "ZDEMO_NEW", "$TMP", "")
	if err != nil {
		t.Fatalf("RenameObject: %v", err)
	}
	if result == nil {
		t.Fatal("RenameObject returned no result")
	}

	calls := rec.snapshot()
	deleteAt := indexOfCall(calls, func(c wireCall) bool { return c.method == http.MethodDelete })
	if deleteAt < 0 {
		t.Fatalf("expected a DELETE of the old object; trace:\n%v", calls)
	}

	released := false
	for _, c := range calls[deleteAt+1:] {
		if isUnlock(c) && strings.EqualFold(c.path, oldURL) {
			released = true
		}
	}
	if !released {
		t.Error("the failed delete left the old object locked: no UNLOCK was sent for it " +
			"after the DELETE failed, so the ENQUEUE outlives the call — on the object the " +
			"caller is being told to delete manually")
		dumpCalls(t, calls)
	}
}

// --- RenameObject source-write target (task_b894bc6f) ---
//
// Step 4 PUT the new object's source to the *bare* object URL instead of
// …/source/main, so SAP answered 400 ExceptionInvalidData ("abapProgram
// previsto") and every rename failed after creating an empty shell.

// renameHappyRoute answers the requests a $TMP PROG rename makes, with the
// source PUT succeeding. override runs first; if it returns true the request
// is considered handled and the default switch is skipped.
func renameHappyRoute(override func(http.ResponseWriter, *http.Request) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if override != nil && override(w, r) {
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor(
				"/sap/bc/adt/programs/programs/zdemo_old", "ZDEMO_OLD", "$TMP"))
		case strings.Contains(r.URL.Path, "nodestructure"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?><asx:abap xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA/></asx:values></asx:abap>`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/source/main"):
			_, _ = io.WriteString(w, "REPORT zdemo_old.\n")
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
}

func TestRenameObject_SourcePutTargetsSourceMain(t *testing.T) {
	const newURL = "/sap/bc/adt/programs/programs/zdemo_new"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, renameHappyRoute(nil), WithAllowedPackages("$TMP"))

	result, err := client.RenameObject(context.Background(), ObjectTypeProgram,
		"ZDEMO_OLD", "ZDEMO_NEW", "$TMP", "")
	if err != nil {
		t.Fatalf("RenameObject: %v", err)
	}
	if !result.Success {
		t.Fatalf("rename did not succeed: %+v", result)
	}

	calls := rec.snapshot()

	putAt := indexOfCall(calls, isSourcePut)
	if putAt < 0 {
		dumpCalls(t, calls)
		t.Fatal("no source PUT ended in /source/main — the write went to the bare object URL (the bug)")
	}
	if got := calls[putAt].path; got != newURL+"/source/main" {
		t.Errorf("source PUT path = %q, want %q", got, newURL+"/source/main")
	}

	// The lock window the PUT sits in must stay stateful, and no networked
	// package lookup may sit between the LOCK and the PUT (issue #91).
	lockAt := -1
	for i := putAt; i >= 0; i-- {
		if isLock(calls[i]) {
			lockAt = i
			break
		}
	}
	if lockAt < 0 {
		t.Fatal("no LOCK before the source PUT")
	}
	assertWindowStateful(t, calls, lockAt, putAt)
	for _, c := range calls[lockAt+1 : putAt] {
		if strings.Contains(c.path, "informationsystem/search") {
			t.Errorf("package SearchObject inside the lock window: %s (issue #91)", c)
		}
	}

	// Order: source PUT (new) → activation → DELETE (old).
	activateAt := indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodPost && strings.Contains(c.path, "/activation")
	})
	deleteAt := indexOfCall(calls, func(c wireCall) bool { return c.method == http.MethodDelete })
	if putAt >= activateAt || activateAt >= deleteAt {
		dumpCalls(t, calls)
		t.Errorf("expected order source-PUT(%d) < activate(%d) < delete-old(%d)", putAt, activateAt, deleteAt)
	}
	if deleteAt >= 0 && !strings.EqualFold(calls[deleteAt].path, "/sap/bc/adt/programs/programs/zdemo_old") {
		t.Errorf("DELETE path = %q, want the old object", calls[deleteAt].path)
	}
}

func TestRenameObject_RejectsUnsupportedType(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, WithAllowedPackages("$TMP"))

	_, err := client.RenameObject(context.Background(), ObjectTypeFunctionGroup,
		"ZDEMO_FG_OLD", "ZDEMO_FG_NEW", "$TMP", "")
	if err == nil {
		t.Fatal("renaming a function group should be rejected up front")
	}
	if !strings.Contains(err.Error(), "FUGR/F") {
		t.Errorf("error should name the rejected type: %v", err)
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		dumpCalls(t, calls)
		t.Errorf("a rejected type must not reach the network (no shell created): %d calls", len(calls))
	}
}

func TestRenameObject_RollsBackShellWhenSourceWriteFails(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, renameHappyRoute(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/source/main") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "ExceptionInvalidData")
			return true
		}
		return false
	}), WithAllowedPackages("$TMP"))

	result, err := client.RenameObject(context.Background(), ObjectTypeProgram,
		"ZDEMO_OLD", "ZDEMO_NEW", "$TMP", "")
	if err != nil {
		t.Fatalf("RenameObject: %v", err)
	}
	if result.Success {
		t.Error("a rename whose source write failed must not report success")
	}

	calls := rec.snapshot()
	rolledBack := indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodDelete &&
			strings.EqualFold(c.path, "/sap/bc/adt/programs/programs/zdemo_new")
	}) >= 0
	if !rolledBack {
		dumpCalls(t, calls)
		t.Error("the partially created ZDEMO_NEW shell was left behind — no DELETE for it")
	}
	if indexOfCall(calls, func(c wireCall) bool {
		return c.method == http.MethodDelete &&
			strings.EqualFold(c.path, "/sap/bc/adt/programs/programs/zdemo_old")
	}) >= 0 {
		t.Error("the old object was deleted even though the rename failed")
	}
}

// A transportable rename with no request named must land the new object's
// source in a real open request (chosen the way the editor would), not let
// SAP auto-generate one per write — the project's transport golden rule.
func TestRenameObject_TransportableRename_AdoptsChosenRequest(t *testing.T) {
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO_NEW",
		checkCandidate{"TR-A", "TESTUSER", "feature A", "D"})

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, checkXML)
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor(
				"/sap/bc/adt/programs/programs/zdemo_old", "ZDEMO_OLD", "ZDEMO_PKG"))
		case strings.Contains(r.URL.Path, "nodestructure"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?><asx:abap xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA><OBJECT/></DATA></asx:values></asx:abap>`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/source/main"):
			_, _ = io.WriteString(w, "REPORT zdemo_old.\n")
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())

	result, err := client.RenameObject(context.Background(), ObjectTypeProgram,
		"ZDEMO_OLD", "ZDEMO_NEW", "ZDEMO_PKG", "")
	if err != nil {
		t.Fatalf("RenameObject: %v", err)
	}
	if !result.Success {
		t.Fatalf("rename did not succeed: %+v", result)
	}
	if result.Transport != "TR-A" {
		t.Errorf("result.Transport = %q, want TR-A (the chosen open request)", result.Transport)
	}
	if result.TransportNote == "" {
		t.Error("result.TransportNote should explain how TR-A was chosen")
	}

	calls := rec.snapshot()
	putAt := indexOfCall(calls, isSourcePut)
	if putAt < 0 {
		dumpCalls(t, calls)
		t.Fatal("no source PUT")
	}
	if got := calls[putAt].query.Get("corrNr"); got != "TR-A" {
		dumpCalls(t, calls)
		t.Errorf("source PUT corrNr = %q, want TR-A — the write did not go into the chosen request", got)
	}
}

func TestReleaseLockAfterFailure_RunsOnACancelledContext(t *testing.T) {
	// A mutation that fails *because* the context was cancelled or timed out
	// must still release its lock. Reusing the dead context, as every
	// compensating unlock in the package used to, fails inside
	// http.NewRequestWithContext and never sends a byte.
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := client.releaseLockAfterFailure(ctx, "/sap/bc/adt/programs/programs/ZDEMO_CANCEL", "HANDLE-1"); err != nil {
		t.Fatalf("releaseLockAfterFailure on a cancelled context: %v", err)
	}

	calls := rec.snapshot()
	if idx := indexOfCall(calls, isUnlock); idx < 0 {
		t.Errorf("no UNLOCK reached the server after the context was cancelled — "+
			"the lock would be stranded until SAP reaps the session; trace: %v", calls)
	}
}

func TestStrandedLockAdvice_SaysWhatTheUserNeeds(t *testing.T) {
	msg := strandedLockAdvice("/sap/bc/adt/oo/classes/ZCL_DEMO_FOO",
		fmt.Errorf("unlocking object: 423 invalid lock handle"))

	for _, want := range []string{
		"ZCL_DEMO_FOO",    // which object
		"your own user",   // not a colleague
		"SM12",            // the immediate route
		"session timeout", // it expires on its own
		"currently editing",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("advice does not mention %q:\n%s", want, msg)
		}
	}
}

// --- The class-delete stranded lock (task_45f386ec) ---
//
// LOCK accessMode=DELETE on a class returns 200 with an empty body on this
// project's S/4HANA: no handle, but the SEOCLSENQ enqueue is taken. The old
// "DELETE mode first" order in DeleteObjectWithAutoLock then 403'd on the
// MODIFY retry and stranded that enqueue on every class delete.

func TestParseLockResult_EmptyBodyIsNoHandle(t *testing.T) {
	for _, body := range []string{"", "   ", "\n\t\n"} {
		if _, err := parseLockResult([]byte(body)); err == nil || !errors.Is(err, errLockNoHandle) {
			t.Errorf("parseLockResult(%q): want errLockNoHandle, got %v", body, err)
		}
	}
	got, err := parseLockResult([]byte(testLockXML))
	if err != nil {
		t.Fatalf("parseLockResult(testLockXML): %v", err)
	}
	if got.LockHandle != "HANDLE-1" {
		t.Errorf("parseLockResult(testLockXML): want HANDLE-1, got %q", got.LockHandle)
	}
}

func TestLockObject_TwoHundredWithNoHandle_IsError(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		// LOCK POST -> 200 OK, empty body (the accessMode=DELETE shape).
		w.WriteHeader(http.StatusOK)
	})

	_, err := client.LockObject(context.Background(), "/sap/bc/adt/oo/classes/ZCL_DEMO_EMPTY", "DELETE")
	if err == nil || !errors.Is(err, errLockNoHandle) {
		t.Fatalf("LockObject on a 2xx-with-no-handle response: want errLockNoHandle, got %v", err)
	}
	if client.lockOutstanding() {
		t.Error("LockObject recorded a lock window for a response that carried no handle")
	}
}

// delModeLockSeen reports whether the trace contains a LOCK with accessMode=DELETE.
func delModeLockSeen(calls []wireCall) bool {
	return indexOfCall(calls, func(c wireCall) bool {
		return isLock(c) && c.query.Get("accessMode") == "DELETE"
	}) >= 0
}

func isLockReqMode(r *http.Request, mode string) bool {
	return r.Method == http.MethodPost &&
		r.URL.Query().Get("_action") == "LOCK" &&
		r.URL.Query().Get("accessMode") == mode
}

func TestDeleteObjectWithAutoLock_ModifyFirst_NeverTriesDeleteMode(t *testing.T) {
	const objectURL = "/sap/bc/adt/oo/classes/ZCL_DEMO_DEL"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case isLockReqMode(r, "DELETE"):
			// If we ever get here the reorder regressed: this is the response
			// that strands the enqueue (200, empty body).
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	if err := client.DeleteObjectWithAutoLock(context.Background(), objectURL, ""); err != nil {
		t.Fatalf("DeleteObjectWithAutoLock: %v", err)
	}

	calls := rec.snapshot()
	if delModeLockSeen(calls) {
		dumpCalls(t, calls)
		t.Fatal("DeleteObjectWithAutoLock issued a LOCK accessMode=DELETE — on this system that " +
			"returns 200 with no handle and strands the SEOCLSENQ enqueue; MODIFY must be tried first")
	}
	if indexOfCall(calls, func(c wireCall) bool { return c.method == http.MethodDelete }) < 0 {
		dumpCalls(t, calls)
		t.Fatal("no DELETE reached the server")
	}
}

func TestDeleteObjectWithAutoLock_FallsBackToDeleteModeWhenModifyRefused(t *testing.T) {
	const objectURL = "/sap/bc/adt/oo/classes/ZCL_DEMO_DELFB"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case isLockReqMode(r, "MODIFY"):
			// A system that will not grant a MODIFY lock for a delete (not a
			// lock conflict — a flat refusal).
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = io.WriteString(w, "MODIFY not allowed for delete")
		case isLockReqMode(r, "DELETE"):
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	if err := client.DeleteObjectWithAutoLock(context.Background(), objectURL, ""); err != nil {
		t.Fatalf("DeleteObjectWithAutoLock (DELETE-mode fallback): %v", err)
	}
	calls := rec.snapshot()
	if !delModeLockSeen(calls) {
		dumpCalls(t, calls)
		t.Fatal("MODIFY was refused but the DELETE-mode fallback was never tried")
	}
}

func TestDeleteObjectWithAutoLock_ModifyConflict_GivesStrandedAdvice(t *testing.T) {
	const objectURL = "/sap/bc/adt/oo/classes/ZCL_DEMO_DELCONF"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK" {
			w.WriteHeader(http.StatusForbidden)
			// The real S/4HANA body: message rendered in the session language
			// (ES here), lock identified by the language-independent key EU/510.
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="ExceptionResourceNoAccess"/><message lang="EN">El usuario TESTUSER ya está tratando ZCL_DEMO_DELCONF .</message><localizedMessage lang="ES">El usuario TESTUSER ya está tratando ZCL_DEMO_DELCONF .</localizedMessage><properties><entry key="T100KEY-ID">EU</entry><entry key="T100KEY-NO">510</entry><entry key="T100KEY-V1">TESTUSER</entry><entry key="T100KEY-V2">ZCL_DEMO_DELCONF</entry></properties></exc:exception>`)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	err := client.DeleteObjectWithAutoLock(context.Background(), objectURL, "")
	if err == nil {
		t.Fatal("expected an error when the MODIFY lock reports a conflict")
	}
	if !strings.Contains(err.Error(), "SM12") {
		t.Errorf("a lock conflict on delete should carry the stranded-lock advice; got: %v", err)
	}
	calls := rec.snapshot()
	if delModeLockSeen(calls) {
		dumpCalls(t, calls)
		t.Error("a MODIFY lock conflict must not fall through to a DELETE-mode attempt " +
			"(that would strand a second enqueue)")
	}
}

func TestDeleteObject_AfterExternalLock_NoSearchInsideWindow(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_EXTDEL"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor(
				"/sap/bc/adt/programs/programs/zdemo_extdel", "ZDEMO_EXTDEL", "$TMP"))
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowedPackages("$TMP"))

	// The pkg/adt callers that lock-then-DeleteObject (RenameObject,
	// cleanupPartialObject) gate + mark the context above the lock. Simulate
	// that here and assert DeleteObject's own gate then issues no SearchObject
	// inside the window.
	ctx, err := client.gateAndMark(context.Background(), MutationContext{
		Op: OpDelete, OpName: "DeleteObject", ObjectURL: objectURL,
	})
	if err != nil {
		t.Fatalf("gateAndMark: %v", err)
	}
	lock, err := client.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	if err := client.DeleteObject(ctx, objectURL, lock.LockHandle, ""); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	deleteAt := indexOfCall(calls, func(c wireCall) bool { return c.method == http.MethodDelete })
	if lockAt < 0 || deleteAt < 0 || deleteAt < lockAt {
		t.Fatalf("expected a LOCK then a DELETE; trace:\n%v", calls)
	}
	for _, c := range calls[lockAt+1 : deleteAt] {
		if strings.Contains(c.path, "informationsystem/search") {
			dumpCalls(t, calls)
			t.Fatal("a package-resolving search ran between the external LOCK and the DELETE — " +
				"it retires the session the lock handle lives in (issue #91); the caller must " +
				"gateAndMark above the lock so DeleteObject's checkMutation skips the lookup")
		}
	}
	assertWindowStateful(t, calls, lockAt, deleteAt)
}

// ADT's program delete handler leaves the ESRDIRE/TRDIR enqueue held on some
// S/4HANA systems (2023 FPS03 confirmed live): the DELETE returns 200 and the
// program is gone, but the lock the MODIFY LOCK took is not released until an
// explicit UNLOCK or the stateful session ends. Both delete paths now send a
// best-effort UNLOCK right after a successful DELETE.

func TestDeleteObjectWithAutoLock_UnlocksAfterDelete(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_DELUNLOCK"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK" {
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := client.DeleteObjectWithAutoLock(context.Background(), objectURL, ""); err != nil {
		t.Fatalf("DeleteObjectWithAutoLock: %v", err)
	}

	calls := rec.snapshot()
	deleteAt := indexOfCall(calls, func(c wireCall) bool { return c.method == http.MethodDelete })
	unlockAt := indexOfCall(calls, isUnlock)
	if deleteAt < 0 {
		dumpCalls(t, calls)
		t.Fatal("no DELETE reached the server")
	}
	if unlockAt < 0 || unlockAt < deleteAt {
		dumpCalls(t, calls)
		t.Fatal("expected an UNLOCK after the DELETE — ADT's program delete leaves the " +
			"ESRDIRE/TRDIR enqueue held until the lock is released explicitly")
	}
	if got := calls[unlockAt].query.Get("lockHandle"); got != "HANDLE-1" {
		t.Errorf("post-delete UNLOCK used lockHandle %q, want the handle the LOCK returned", got)
	}
	if calls[unlockAt].sessionType != "stateful" {
		t.Errorf("post-delete UNLOCK is %q, want stateful (it must reach the session the "+
			"lock lives in, not retire it)", calls[unlockAt].sessionType)
	}
	if client.lockOutstanding() {
		t.Error("lock window still open after DeleteObjectWithAutoLock + UNLOCK")
	}
}

func TestDeleteObject_UnlocksAfterDelete(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_DOUNLOCK"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if err := client.DeleteObject(context.Background(), objectURL, "EXT-HANDLE", ""); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	calls := rec.snapshot()
	deleteAt := indexOfCall(calls, func(c wireCall) bool { return c.method == http.MethodDelete })
	unlockAt := indexOfCall(calls, isUnlock)
	if deleteAt < 0 || unlockAt < 0 || unlockAt < deleteAt {
		dumpCalls(t, calls)
		t.Fatal("DeleteObject should send a best-effort UNLOCK after the DELETE")
	}
	if got := calls[unlockAt].query.Get("lockHandle"); got != "EXT-HANDLE" {
		t.Errorf("post-delete UNLOCK used lockHandle %q, want the caller's handle", got)
	}
}

func TestDeleteObjectWithAutoLock_UnlockFailureDoesNotFailDelete(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_DELUNLOCKFAIL"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "UNLOCK":
			// A stricter system that rejects the already-consumed handle.
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	if err := client.DeleteObjectWithAutoLock(context.Background(), objectURL, ""); err != nil {
		t.Fatalf("a failed post-delete UNLOCK must not fail the delete: %v", err)
	}
	if indexOfCall(rec.snapshot(), isUnlock) < 0 {
		t.Error("the post-delete UNLOCK was never attempted")
	}
}

func TestBestEffortUnlockAfterDelete_EmptyHandleSkipsTheUnlock(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	client.bestEffortUnlockAfterDelete(context.Background(), "/sap/bc/adt/programs/programs/ZDEMO_NOHANDLE", "")

	if idx := indexOfCall(rec.snapshot(), isUnlock); idx >= 0 {
		t.Error("bestEffortUnlockAfterDelete issued an UNLOCK for an empty lock handle")
	}
}

func TestBestEffortUnlockAfterDelete_RunsOnACancelledContext(t *testing.T) {
	// Same property as releaseLockAfterFailure: the caller's ctx being done
	// (MCP client timeout, Ctrl-C) must not stop the compensating UNLOCK.
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client.bestEffortUnlockAfterDelete(ctx, "/sap/bc/adt/programs/programs/ZDEMO_DELCANCEL", "HANDLE-1")

	if idx := indexOfCall(rec.snapshot(), isUnlock); idx < 0 {
		t.Errorf("no UNLOCK reached the server after the context was cancelled — "+
			"the PROG enqueue would be stranded until SAP reaps the session; trace: %v", rec.snapshot())
	}
}

func TestIsLockConflictError_LanguageIndependent(t *testing.T) {
	// The real S/4HANA 403 — message rendered in ES, lock keyed by EU/510.
	esBody := `403 at /x: <exc:exception><type id="ExceptionResourceNoAccess"/>` +
		`<message lang="EN">El usuario TESTUSER ya está tratando ZCL_X .</message>` +
		`<properties><entry key="T100KEY-ID">EU</entry><entry key="T100KEY-NO">510</entry></properties></exc:exception>`
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"ES message + EU/510", fmt.Errorf("locking object: ADT API error: status %s", esBody), true},
		{"EN currently editing", fmt.Errorf("status 403: user is currently editing ZCL_X"), true},
		{"nil", nil, false},
		{"403 but not a lock", fmt.Errorf("status 403: ExceptionResourceNotFound"), false},
		{"lock text but no 403", fmt.Errorf("ya está tratando"), false},
	}
	for _, c := range cases {
		if got := isLockConflictError(c.err); got != c.want {
			t.Errorf("%s: isLockConflictError = %v, want %v", c.name, got, c.want)
		}
	}
}

// --- The exported preflight used by the MCP deploy handlers ---

func TestPrepareSourceUpdate_MarksTheObjectItChecked(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "informationsystem/search") {
			_, _ = io.WriteString(w, searchXMLFor(
				"/sap/bc/adt/programs/programs/zdemo_prep", "ZDEMO_PREP", "$TMP"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}, WithAllowedPackages("$TMP"))

	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_PREP"

	ctx, err := client.PrepareSourceUpdate(context.Background(), objectURL, "")
	if err != nil {
		t.Fatalf("PrepareSourceUpdate: %v", err)
	}
	before := len(rec.snapshot())

	if err := client.checkMutation(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "UpdateSource",
		ObjectURL: objectURL + "/source/main",
	}); err != nil {
		t.Fatalf("gate rejected the prepared object: %v", err)
	}
	if after := len(rec.snapshot()); after != before {
		t.Errorf("the write's own gate issued %d more request(s) after PrepareSourceUpdate; "+
			"inside a lock window each one costs the lock handle", after-before)
	}

	// A different object gets no free pass: the prepared context must not
	// suppress the lookup for anything but the object it checked.
	before = len(rec.snapshot())
	_ = client.checkMutation(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "UpdateSource",
		ObjectURL: "/sap/bc/adt/programs/programs/ZDEMO_ELSEWHERE/source/main",
	})
	if after := len(rec.snapshot()); after == before {
		t.Error("an unmarked object was accepted without resolving its package")
	}
}

// --- PR #203 port: the FUNC update branch never adopted lock.CorrNr
// (issue #144) even before the transport-choice port — a plain omission,
// not shared by any structural difference from the INTF/DDLS/BDEF/SRVD
// branches right above it in the same function, which already had the
// fix. Both close together in the same change. ---

const testLockXMLWithTransport = `<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><CORRNR>TR-EXAMPLE</CORRNR>
<MODIFICATION_SUPPORT>NoModification</MODIFICATION_SUPPORT>
</DATA></asx:values></asx:abap>`

func TestWriteSourceFUNC_AdoptsLockCorrNrWhenTransportNotSupplied(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXMLWithTransport)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())

	result, err := client.writeSourceUpdate(context.Background(), "FUNC", "Z_DEMO_FM",
		"FUNCTION z_demo_fm.\nENDFUNCTION.\n", &WriteSourceOptions{Parent: "ZFG_DEMO"})
	if err != nil {
		t.Fatalf("writeSourceUpdate(FUNC): %v", err)
	}
	if !result.Success {
		t.Fatalf("update did not succeed: %s", result.Message)
	}
	if result.Transport != "TR-EXAMPLE" {
		t.Errorf("Transport = %q, want the lock's own request TR-EXAMPLE (issue #144) — "+
			"this branch never adopted lock.CorrNr before the #203 port", result.Transport)
	}

	calls := rec.snapshot()
	putAt := indexOfCall(calls, isSourcePut)
	if putAt < 0 {
		t.Fatalf("expected a PUT of the function module source; trace:\n%v", calls)
	}
	if got := calls[putAt].query.Get("corrNr"); got != "TR-EXAMPLE" {
		t.Errorf("source PUT corrNr = %q, want TR-EXAMPLE; trace:\n%v", got, calls)
	}
}

func TestWriteSourceFUNC_SourcePutStaysInTheLockSession(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	result, err := client.writeSourceUpdate(context.Background(), "FUNC", "Z_DEMO_FM",
		"FUNCTION z_demo_fm.\nENDFUNCTION.\n", &WriteSourceOptions{Parent: "ZFG_DEMO"})
	if err != nil {
		t.Fatalf("writeSourceUpdate(FUNC): %v", err)
	}
	if !result.Success {
		t.Fatalf("update did not succeed: %s", result.Message)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	putAt := indexOfCall(calls, isSourcePut)
	if lockAt < 0 || putAt < 0 || putAt < lockAt {
		t.Fatalf("expected a LOCK followed by a source PUT; trace:\n%v", calls)
	}
	assertWindowStateful(t, calls, lockAt, putAt)
}

// TestGetSource_NoCache_BypassesResponseCache pins a code-review finding on
// the #191 (source-hash) / #199 (response cache) interaction: GetSourceOptions.
// NoCache promises "a fresh read from SAP" for callers establishing a
// hash baseline before a guarded write, by skipping the client's own
// sourceCache — but without also emptying the transport's response cache
// (VSP_CACHE), a caller with that enabled could still get a body served
// straight out of it, silently defeating the "fresh" promise.
func TestGetSource_NoCache_BypassesResponseCache(t *testing.T) {
	var hits atomic.Int64
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "REPORT z_demo_probe.")
	}, WithCache(time.Minute))

	ctx := context.Background()
	if _, err := client.GetSource(ctx, "PROG", "Z_DEMO_PROBE", &GetSourceOptions{}); err != nil {
		t.Fatalf("first read: %v", err)
	}
	// A second plain read within the TTL should not reach the server at
	// all: sourceCache serves it first.
	if _, err := client.GetSource(ctx, "PROG", "Z_DEMO_PROBE", &GetSourceOptions{}); err != nil {
		t.Fatalf("second (cached) read: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("plain reads: server saw %d requests, want 1 (sourceCache should have served the second)", n)
	}
	// NoCache skips sourceCache — and must also bypass the response cache,
	// or this still would not reach the server.
	if _, err := client.GetSource(ctx, "PROG", "Z_DEMO_PROBE", &GetSourceOptions{NoCache: true}); err != nil {
		t.Fatalf("NoCache read: %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("NoCache read: server saw %d requests total, want 2 — NoCache did not reach SAP, "+
			"it was served from the response cache instead", n)
	}
}
