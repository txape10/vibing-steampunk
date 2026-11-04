package adt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A write that needs a transport request and cannot get one is refused before
// the LOCK: sent without a corrNr, the LOCK lets SAP generate a request of its
// own ("Generated Request for Change Recording").

func anyLockSent(rec *adtRecorder) bool {
	return indexOfCall(rec.snapshot(), isLock) >= 0
}

// The check says a request is needed, none of the user's fits, and creating
// one is not enabled.
func TestPlanTransport_RequestNeededNoneFits_Refuses(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "cts/transportchecks") {
			_, _ = io.WriteString(w, transportCheckXML(true, "ZDEMO_PKG", "ZDEMO"))
		}
	})

	plan := client.planTransport(context.Background(), "", "/sap/bc/adt/programs/programs/zdemo", "ZDEMO_PKG")
	if plan == nil || !errors.Is(plan.Err, errTransportRequired) {
		t.Fatalf("plan = %+v, want a refusal (errTransportRequired)", plan)
	}
	if _, err := plan.lockCorrNr(""); !errors.Is(err, errTransportRequired) {
		t.Errorf("lockCorrNr err = %v, want errTransportRequired", err)
	}
}

// The check itself fails and the object is not known to be local.
func TestPlanTransport_CheckFailsOnTransportableObject_Refuses(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "cts/transportchecks") {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	plan := client.planTransport(context.Background(), "", "/sap/bc/adt/programs/programs/zdemo", "ZDEMO_PKG")
	if plan == nil || !errors.Is(plan.Err, errTransportRequired) {
		t.Fatalf("plan = %+v, want a refusal (errTransportRequired)", plan)
	}
}

// Control: the same failing check on a local package is not a refusal.
func TestPlanTransport_CheckFailsOnLocalObject_StillWrites(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "cts/transportchecks") {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	plan := client.planTransport(context.Background(), "", "/sap/bc/adt/programs/programs/zdemo", "$TMP")
	if plan == nil || plan.Err != nil {
		t.Fatalf("plan = %+v, want no refusal for a $ package", plan)
	}
	if _, err := plan.lockCorrNr(""); err != nil {
		t.Errorf("lockCorrNr err = %v, want nil", err)
	}
}

// An existing object (no package passed): the package is resolved when the
// check fails, and a local one is not refused.
func TestPlanTransport_CheckFailsPackageResolvedLocal_StillWrites(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			w.WriteHeader(http.StatusInternalServerError)
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor("/sap/bc/adt/programs/programs/zdemo", "ZDEMO", "$TMP"))
		}
	})

	plan := client.planTransport(context.Background(), "", "/sap/bc/adt/programs/programs/zdemo", "")
	if plan == nil || plan.Err != nil {
		t.Fatalf("plan = %+v, want no refusal for an object resolved to $TMP", plan)
	}
}

// An explicit request, or the explicit opt-out, is never a refusal.
func TestPlanTransport_NamedOrOptOut_NeverRefuses(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {})
	if plan := client.planTransport(context.Background(), "TR-NAMED", "/x", "ZDEMO_PKG"); plan != nil {
		t.Errorf("a named transport produced a plan: %+v", plan)
	}
}

// The workflow level: no LOCK reaches SAP when the plan refuses.
func TestWriteProgram_PlanRefuses_NoLockSent(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, transportCheckXML(true, "ZDEMO_PKG", "ZDEMO_REF"))
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor("/sap/bc/adt/programs/programs/zdemo_ref", "ZDEMO_REF", "ZDEMO_PKG"))
		case strings.Contains(r.URL.Path, "/checkruns"):
			_, _ = io.WriteString(w, testEmptyCheckXML)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			_, _ = io.WriteString(w, testLockXML)
		}
	}, WithAllowTransportableEdits())

	result, _ := client.WriteProgram(context.Background(), "ZDEMO_REF", "REPORT zdemo_ref.\n", "")
	if result != nil && result.Success {
		t.Fatal("WriteProgram succeeded although no request could be chosen")
	}
	if anyLockSent(rec) {
		dumpCalls(t, rec.snapshot())
		t.Fatal("a LOCK was sent without a transport; SAP could have generated a request")
	}
	if result == nil || !strings.Contains(result.Message, "transport") {
		t.Errorf("result message does not explain the refusal: %+v", result)
	}
}

// The lock-scope path refuses the same way and returns the sentinel.
func TestWithObjectLock_PlanRefuses_NoLockSent(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "cts/transportchecks") {
			_, _ = io.WriteString(w, transportCheckXML(true, "ZDEMO_PKG", "ZDEMO_SCOPE"))
			return
		}
		lockScopeRoute(w, r)
	}, WithAllowTransportableEdits())

	called := false
	err := client.WithObjectLock(context.Background(), "UpdateSource", "/sap/bc/adt/programs/programs/ZDEMO_SCOPE", "",
		func(ctx context.Context, handle, transport string) error { called = true; return nil })
	if !errors.Is(err, errTransportRequired) {
		t.Fatalf("err = %v, want errTransportRequired", err)
	}
	if called || anyLockSent(rec) {
		dumpCalls(t, rec.snapshot())
		t.Fatal("the lock was taken although the plan refused")
	}
}

// Delete locks an existing object too: it must not reach SAP without a request
// when one is needed and none can be chosen.
func TestDeleteObjectWithAutoLock_PlanRefuses_NoLockSent(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, transportCheckXML(true, "ZDEMO_PKG", "ZDEMO_DEL"))
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor("/sap/bc/adt/programs/programs/zdemo_del", "ZDEMO_DEL", "ZDEMO_PKG"))
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			_, _ = io.WriteString(w, testLockXML)
		}
	}, WithAllowTransportableEdits())

	err := client.DeleteObjectWithAutoLock(context.Background(), "/sap/bc/adt/programs/programs/zdemo_del", "")
	if !errors.Is(err, errTransportRequired) {
		t.Fatalf("err = %v, want errTransportRequired", err)
	}
	if anyLockSent(rec) {
		dumpCalls(t, rec.snapshot())
		t.Fatal("a LOCK for delete was sent without a transport")
	}
}

// Control: an explicit transport is never refused by the plan.
func TestDeleteObjectWithAutoLock_NamedTransport_LockCarriesIt(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, searchXMLFor("/sap/bc/adt/programs/programs/zdemo_del", "ZDEMO_DEL", "ZDEMO_PKG"))
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			_, _ = io.WriteString(w, testLockXML)
		}
	}, WithAllowTransportableEdits())

	_ = client.DeleteObjectWithAutoLock(context.Background(), "/sap/bc/adt/programs/programs/zdemo_del", "TR-NAMED")
	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	if lockAt < 0 || calls[lockAt].query.Get("corrNr") != "TR-NAMED" {
		dumpCalls(t, calls)
		t.Fatal("the delete LOCK did not carry the named transport")
	}
}
