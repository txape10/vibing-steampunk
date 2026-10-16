package adt

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClassifyExecuteRun(t *testing.T) {
	ran := &UnitTestResult{Classes: []UnitTestClass{{TestMethods: []UnitTestMethod{{Name: "EXECUTE_PAYLOAD"}}}}}
	refused := &UnitTestResult{Classes: []UnitTestClass{{
		Name: "LTC_EXECUTOR",
		Alerts: []UnitTestAlert{{
			Kind: "warning", Severity: "tolerable",
			Title:   "No execution, risk level of test class exceeds upper limit",
			Details: []string{"allowed: harmless"},
		}},
	}}}

	if got := classifyExecuteRun(ran); got != nil {
		t.Errorf("a run with a test method is not a failure, got %+v", got)
	}
	if got := classifyExecuteRun(&UnitTestResult{}); got == nil || got.Kind != "notRun" {
		t.Errorf("no test class must be notRun, got %+v", got)
	}
	if got := classifyExecuteRun(nil); got == nil || got.Kind != "notRun" {
		t.Errorf("a nil result must be notRun, got %+v", got)
	}
	got := classifyExecuteRun(refused)
	if got == nil || got.Kind != "notRun" {
		t.Fatalf("a class ABAP Unit refused must be notRun, got %+v", got)
	}
	if !strings.Contains(got.Title, "risk level") {
		t.Errorf("the title must be ABAP Unit's own reason, got %q", got.Title)
	}
}

const cleanupObjectURL = "/sap/bc/adt/programs/programs/ztemp_exec_00000001"

// The cleanup of a run that ended because the caller's context was cancelled
// still has to reach SAP: it runs on a detached context.
func TestDeleteTemporaryProgram_SurvivesCancelledCaller(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case isLock(wireCallOf(r)):
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	caller, cancel := context.WithCancel(context.Background())
	caller = withMutationPackageChecked(caller, cleanupObjectURL)
	cancel()

	cleanupCtx, release := failureCleanupContext(caller)
	defer release()
	if warnings := client.deleteTemporaryProgram(cleanupCtx, cleanupObjectURL, "ZTEMP_EXEC_00000001"); len(warnings) != 0 {
		t.Fatalf("cleanup reported warnings on a healthy system: %v", warnings)
	}

	calls := rec.snapshot()
	if indexOfCall(calls, isLock) < 0 || indexOfCall(calls, func(c wireCall) bool { return c.method == http.MethodDelete }) < 0 {
		dumpCalls(t, calls)
		t.Fatal("the cleanup LOCK and DELETE never left the process")
	}
	for _, c := range calls {
		if strings.Contains(c.path, "informationsystem/search") {
			dumpCalls(t, calls)
			t.Fatal("the package lookup ran again inside the lock window; the marker was lost")
		}
	}
}

func TestDeleteTemporaryProgram_ReportsAFailedLock(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if isLock(wireCallOf(r)) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	ctx := withMutationPackageChecked(context.Background(), cleanupObjectURL)
	warnings := client.deleteTemporaryProgram(ctx, cleanupObjectURL, "ZTEMP_EXEC_00000001")
	if len(warnings) == 0 || !strings.Contains(warnings[0], "ZTEMP_EXEC_00000001") {
		t.Fatalf("a failed cleanup LOCK must be reported by program name, got %v", warnings)
	}
}

func TestDeleteTemporaryProgram_FailedDeleteReleasesTheLock(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case isLock(wireCallOf(r)):
			_, _ = io.WriteString(w, testLockXML)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
	ctx := withMutationPackageChecked(context.Background(), cleanupObjectURL)
	warnings := client.deleteTemporaryProgram(ctx, cleanupObjectURL, "ZTEMP_EXEC_00000001")
	if len(warnings) == 0 {
		t.Fatal("a failed DELETE must be reported")
	}
	if indexOfCall(rec.snapshot(), isUnlock) < 0 {
		t.Error("the lock of the failed DELETE was not released")
	}
	if msg := appendExecuteCleanupWarning("", warnings); !strings.HasPrefix(msg, "Cleanup warning: ") {
		t.Errorf("unexpected warning text %q", msg)
	}
}

func wireCallOf(r *http.Request) wireCall {
	return wireCall{method: r.Method, path: r.URL.Path, query: r.URL.Query(), sessionType: r.Header.Get("X-sap-adt-sessiontype")}
}

// The defer of ExecuteABAP itself: the caller's context is cancelled while the
// source is being written, and the temporary program is still locked, deleted
// and reported as cleaned up.
func TestExecuteABAP_CleanupRunsAfterCallerCancellation(t *testing.T) {
	rec := &adtRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case isLock(wireCallOf(r)):
			_, _ = io.WriteString(w, testLockXML)
		case isSourcePut(wireCallOf(r)):
			cancel()
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	result, err := client.ExecuteABAP(ctx, "lv_result = 'x'.", nil)
	if err != nil {
		t.Fatalf("ExecuteABAP: %v", err)
	}
	calls := rec.snapshot()
	if indexOfCall(calls, func(c wireCall) bool { return c.method == http.MethodDelete }) < 0 {
		dumpCalls(t, calls)
		t.Fatal("the cleanup DELETE never left the process after the caller's context was cancelled")
	}
	if !result.CleanedUp {
		t.Errorf("CleanedUp = false, message %q", result.Message)
	}
}
