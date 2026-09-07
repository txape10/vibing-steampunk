package adt

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestMutationGateSkip_EditSourceNoSearchBetweenLockAndPut is the
// regression guard for the sister bug of commit 8cb45a5 (SyntaxCheck
// before Lock).
//
// The original symptom: when AllowedPackages was configured, EditSource
// produced an HTTP 423 ExceptionResourceInvalidLockHandle on the PUT
// because the inner UpdateSource gate ran getObjectPackage →
// SearchObject (a STATELESS hop) between the stateful Lock and the
// stateful PUT. SAP's ICM retired the stateful session on the stateless
// hop (Sap-Err-Id: ICMENOSESSION), and the lock handle bound to that
// dead session was then rejected by the PUT.
//
// The fix marks the context (via gateAndMark/withMutationPackageChecked, see
// mutation_gate_marker.go) after the outer EditSourceWithOptions gate
// completes; the inner UpdateSource sees the object is already marked and
// skips its own getObjectPackage call. This test pins the absence of any
// informationsystem/search request between LOCK and PUT in the call
// sequence.
func TestMutationGateSkip_EditSourceNoSearchBetweenLockAndPut(t *testing.T) {
	const sourceBody = "REPORT ztest.\nWRITE / 'hello'.\n"
	const newSourceBody = "REPORT ztest.\nWRITE / 'world'.\n"

	mock := &methodPathMock{
		routes: []routedResponse{
			resp("", "discovery", 200, "ok"),
			// Outer gate's package resolution — happens BEFORE Lock.
			resp("", "informationsystem/search", 200, searchZTESTInTmpXML),
			// GET source/main → return current source.
			resp(http.MethodGet, "/programs/programs/ZTEST/source/main", 200, sourceBody),
			// SyntaxCheck (POST checkruns) → return clean.
			resp(http.MethodPost, "checkruns", 200, ""),
			// Lock acquisition (POST with _action=LOCK).
			resp(http.MethodPost, "/programs/programs/ZTEST", 200, lockResponseXML),
			// PUT source/main → succeed.
			resp(http.MethodPut, "/programs/programs/ZTEST/source/main", 200, ""),
			// Activate.
			resp(http.MethodPost, "/activation", 200, ""),
		},
	}
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass",
		WithAllowedPackages("$TMP"),
		// The lock response carries a real corrNr; transport adoption
		// re-validates against this policy (issue #144 follow-up), so it
		// must be allowed here for the PUT this test asserts on to be reached.
		WithAllowTransportableEdits(),
	)
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	result, err := client.EditSourceWithOptions(
		context.Background(),
		"/sap/bc/adt/programs/programs/ZTEST",
		"WRITE / 'hello'.",
		"WRITE / 'world'.",
		&EditSourceOptions{SyntaxCheck: false},
	)
	if err != nil {
		t.Fatalf("EditSourceWithOptions failed: %v", err)
	}
	_ = result
	_ = newSourceBody

	// Walk the recorded calls, find the Lock POST and the source PUT,
	// and assert no informationsystem/search appears between them.
	lockIdx, putIdx := -1, -1
	for i, c := range mock.calls {
		if c.method == http.MethodPost && strings.HasSuffix(c.path, "/programs/programs/ZTEST") {
			// LockObject POSTs to the bare object URL with _action=LOCK
			// in the query string. The mock records the path, not the
			// query, so this is the right match.
			lockIdx = i
		}
		if c.method == http.MethodPut && strings.HasSuffix(c.path, "/programs/programs/ZTEST/source/main") {
			putIdx = i
			break
		}
	}
	if lockIdx == -1 {
		t.Fatalf("no Lock POST observed; calls: %+v", mock.calls)
	}
	if putIdx == -1 {
		t.Fatalf("no source PUT observed; calls: %+v", mock.calls)
	}
	if lockIdx >= putIdx {
		t.Fatalf("lock should precede put; lockIdx=%d putIdx=%d calls=%+v",
			lockIdx, putIdx, mock.calls)
	}

	for i := lockIdx + 1; i < putIdx; i++ {
		if strings.Contains(mock.calls[i].path, "informationsystem/search") {
			t.Errorf(
				"informationsystem/search hop appeared between Lock and PUT (index %d): %s — "+
					"this is the session-affinity regression: the stateless search "+
					"retires SAP's stateful session and invalidates the lock handle "+
					"(sister bug of commit 8cb45a5)",
				i, mock.calls[i].path,
			)
		}
	}
}

// The old mutationGateSkipKey boolean (all 3 checks skipped as a unit) and
// its two unit tests (TestMutationGateSkip_FlagSkipsInnerCheck,
// TestMutationGateSkip_FlagDoesNotLeakAcrossContexts) were retired when the
// gate moved to the per-object marker in mutation_gate_marker.go — see
// TestMutationMarker_* in session_affinity_test.go for the marker's
// equivalent (and stricter — policy-preserving) coverage.
