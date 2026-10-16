package adt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func lockScopeRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "informationsystem/search"):
		_, _ = io.WriteString(w, searchXMLFor(
			"/sap/bc/adt/programs/programs/zdemo_scope", "ZDEMO_SCOPE", "$TMP"))
	case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
		w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
		_, _ = io.WriteString(w, testLockXML)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func TestWithObjectLock_LocksWritesAndUnlocksInOneStatefulWindow(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_SCOPE"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, lockScopeRoute, WithAllowedPackages("$TMP"))

	var gotHandle string
	err := client.WithObjectLock(context.Background(), "UpdateSource", objectURL, "",
		func(ctx context.Context, handle, transport string) error {
			gotHandle = handle
			return client.UpdateSource(ctx, objectURL+"/source/main", "REPORT zdemo_scope.\n", handle, transport)
		})
	if err != nil {
		t.Fatalf("WithObjectLock: %v", err)
	}
	if gotHandle != "HANDLE-1" {
		t.Errorf("fn got handle %q, want the one the LOCK returned", gotHandle)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	putAt := indexOfCall(calls, isSourcePut)
	unlockAt := indexOfCall(calls, isUnlock)
	if lockAt < 0 || putAt < lockAt || unlockAt < putAt {
		dumpCalls(t, calls)
		t.Fatal("expected LOCK, source PUT, UNLOCK in that order")
	}
	assertWindowStateful(t, calls, lockAt, putAt)
	// The package lookup must have happened above the LOCK, never inside it.
	if searchAt := indexOfCall(calls, func(c wireCall) bool {
		return strings.Contains(c.path, "informationsystem/search")
	}); searchAt > lockAt {
		dumpCalls(t, calls)
		t.Error("package lookup ran after the LOCK")
	}
	if client.lockOutstanding() {
		t.Error("lock window still open after the call")
	}
}

func TestWithObjectLock_FailureReleasesTheLock(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_SCOPE"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, lockScopeRoute)

	boom := errors.New("write refused")
	err := client.WithObjectLock(context.Background(), "UpdateSource", objectURL, "",
		func(ctx context.Context, handle, transport string) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the callback's error", err)
	}

	calls := rec.snapshot()
	if indexOfCall(calls, isUnlock) < 0 {
		dumpCalls(t, calls)
		t.Fatal("a failed write left the object locked")
	}
}

func TestWithObjectLock_ReleasesOnCancelledContext(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_SCOPE"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, lockScopeRoute)

	ctx, cancel := context.WithCancel(context.Background())
	_ = client.WithObjectLock(ctx, "UpdateSource", objectURL, "",
		func(ctx context.Context, handle, transport string) error {
			cancel()
			return ctx.Err()
		})
	if indexOfCall(rec.snapshot(), isUnlock) < 0 {
		dumpCalls(t, rec.snapshot())
		t.Fatal("cancelled context stranded the lock")
	}
}

func TestWithObjectLock_PolicyRefusalSendsNothing(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, lockScopeRoute, WithReadOnly())

	called := false
	err := client.WithObjectLock(context.Background(), "UpdateSource",
		"/sap/bc/adt/programs/programs/ZDEMO_SCOPE", "",
		func(ctx context.Context, handle, transport string) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("read-only must refuse before locking (err=%v, called=%v)", err, called)
	}
	if indexOfCall(rec.snapshot(), isLock) >= 0 {
		t.Error("a LOCK reached SAP under read-only")
	}
}

func TestWithObjectLock_PutsTransportOnTheLock(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_SCOPE"

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, lockScopeRoute, WithEnableTransports(), WithAllowTransportableEdits())

	var gotTransport string
	err := client.WithObjectLock(context.Background(), "UpdateSource", objectURL, "TR-EXAMPLE",
		func(ctx context.Context, handle, transport string) error { gotTransport = transport; return nil })
	if err != nil {
		t.Fatalf("WithObjectLock: %v", err)
	}
	if gotTransport != "TR-EXAMPLE" {
		t.Errorf("fn got transport %q, want TR-EXAMPLE", gotTransport)
	}
	calls := rec.snapshot()
	if i := indexOfCall(calls, isLock); i < 0 || calls[i].query.Get("corrNr") != "TR-EXAMPLE" {
		dumpCalls(t, calls)
		t.Error("the LOCK did not carry the transport")
	}
}

func TestWithObjectLock_NoTransportNamed_PlansOneBeforeTheLock(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_SCOPE"
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO_SCOPE",
		checkCandidate{"TR-A", "TESTUSER", "feature A", "D"})

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "cts/transportchecks") {
			_, _ = io.WriteString(w, checkXML)
			return
		}
		lockScopeRoute(w, r)
	}, WithAllowTransportableEdits())

	var gotTransport string
	err := client.WithObjectLock(context.Background(), "UpdateSource", objectURL, "",
		func(ctx context.Context, handle, transport string) error { gotTransport = transport; return nil })
	if err != nil {
		t.Fatalf("WithObjectLock: %v", err)
	}
	if gotTransport != "TR-A" {
		t.Errorf("fn got transport %q, want TR-A (the open request the plan chose)", gotTransport)
	}
	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	if lockAt < 0 || calls[lockAt].query.Get("corrNr") != "TR-A" {
		dumpCalls(t, calls)
		t.Fatal("the LOCK did not carry the planned request, so SAP could generate its own")
	}
	checkAt := indexOfCall(calls, func(c wireCall) bool { return strings.Contains(c.path, "cts/transportchecks") })
	if checkAt < 0 || checkAt > lockAt {
		dumpCalls(t, calls)
		t.Error("the transport check must run before the LOCK")
	}
}
