package adt

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSourceHashNormalizesADTLineEndingsAndTrailingBlankLines(t *testing.T) {
	lf := "REPORT zdemo.\nWRITE 'x'.\n"
	adtMaterialized := "REPORT zdemo.\r\nWRITE 'x'.\r\n\r\n"
	if got, want := SourceHash(adtMaterialized), SourceHash(lf); got != want {
		t.Fatalf("SourceHash must ignore ADT line-ending/trailing-newline normalization: got %s, want %s", got, want)
	}
}

// TestUpdateSourceRejectsDriftBeforePut and TestUpdateSourceAcceptsMatchingVersionAndWrites
// are adapted from upstream PR #191 to this fork's own recording harness
// (newStubbedClient/adtRecorder, from session_affinity_test.go) instead of a
// bare httptest.NewServer, so the drift check is exercised through the same
// wire-level assertions the rest of the lock-window/session-affinity suite
// uses. No AllowedPackages are configured, so checkMutation's package lookup
// is a no-op and the GET/PUT sequence is all that needs stubbing.
const sourceHashTestURL = "/sap/bc/adt/programs/programs/zdemo/source/main"

func TestUpdateSourceRejectsDriftBeforePut(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte("REPORT zdemo.\nWRITE 'changed elsewhere'.\n"))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	ctx := withExpectedSourceHash(context.Background(), SourceHash("REPORT zdemo.\nWRITE 'original'.\n"))
	err := client.UpdateSource(ctx, sourceHashTestURL, "REPORT zdemo.\nWRITE 'replacement'.\n", "HANDLE-1", "")
	if err == nil {
		t.Fatal("UpdateSource must reject a changed source")
	}
	if _, ok := err.(*SourceDriftError); !ok {
		t.Fatalf("UpdateSource error = %T (%v), want *SourceDriftError", err, err)
	}

	calls := rec.snapshot()
	for _, c := range calls {
		if c.method == http.MethodPut {
			t.Fatalf("drift must be rejected before PUT; got a PUT call: %s", c)
		}
	}
}

func TestUpdateSourceAcceptsMatchingVersionAndWrites(t *testing.T) {
	const current = "REPORT zdemo.\nWRITE 'original'.\n"
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(current))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	ctx := withExpectedSourceHash(context.Background(), SourceHash(current))
	if err := client.UpdateSource(ctx, sourceHashTestURL, "REPORT zdemo.\nWRITE 'replacement'.\n", "HANDLE-1", ""); err != nil {
		t.Fatalf("UpdateSource returned an error for a matching version: %v", err)
	}

	putCalls := 0
	for _, c := range rec.snapshot() {
		if c.method == http.MethodPut {
			putCalls++
		}
	}
	if putCalls != 1 {
		t.Fatalf("matching version should write once; got %d PUT calls", putCalls)
	}
}

// TestVerifyExpectedSourceHash_ConsumedOnlyOnce pins the single-use design
// verifyExpectedSourceHash's own doc comment describes: the expectation must
// be consumed by the first source write in a workflow, so a class's optional
// test-include update (writeSourceUpdate's CLAS+TestSource branch, which
// calls UpdateClassInclude with the same ctx after WriteClass already
// consumed the expectation via UpdateSource) does not compare its own,
// necessarily-different, source against the main object's hash. Exercised
// directly against verifyExpectedSourceHash rather than through the full
// CLAS+TestSource workflow, which needs a heavier multi-endpoint stub for a
// property this pins just as precisely at the unit level.
func TestVerifyExpectedSourceHash_ConsumedOnlyOnce(t *testing.T) {
	const original = "REPORT zdemo.\nWRITE 'original'.\n"
	rec := &adtRecorder{}
	getCalls := 0
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getCalls++
			_, _ = w.Write([]byte(original))
		}
	})

	ctx := withExpectedSourceHash(context.Background(), SourceHash(original))
	if err := client.verifyExpectedSourceHash(ctx, sourceHashTestURL); err != nil {
		t.Fatalf("first verification should succeed: %v", err)
	}
	if getCalls != 1 {
		t.Fatalf("expected exactly 1 verification GET, got %d", getCalls)
	}

	// A second write sharing the same ctx (e.g. the test-include update that
	// follows a class body update) must not re-verify — even against a URL
	// whose content would fail the check if it were compared again. If the
	// mutex+used guard regressed, this would return a *SourceDriftError.
	if err := client.verifyExpectedSourceHash(ctx, sourceHashTestURL); err != nil {
		t.Fatalf("second verification on an already-used expectation must be a no-op, got: %v", err)
	}
	if getCalls != 1 {
		t.Fatalf("second call must not perform another verification GET; got %d total", getCalls)
	}
}

// TestWriteSource_FUNC_RejectsExpectedSourceHash pins the guard added for
// Decision B of the PR #191 port: this fork has no dedicated
// writeSourceFunctionModule dispatch (FUNC is handled inline in
// writeSourceCreate/writeSourceUpdate), so the block lives at FUNC's other
// precondition checks in WriteSource itself, before any network call.
func TestWriteSource_FUNC_RejectsExpectedSourceHash(t *testing.T) {
	c := NewClient("http://example.invalid", "TESTUSER", "pw")
	result, err := c.WriteSource(context.Background(), "FUNC", "Z_TEST_FM",
		"FUNCTION z_test_fm.\nENDFUNCTION.\n", &WriteSourceOptions{
			Parent:             "ZTEST_FUGR",
			ExpectedSourceHash: SourceHash("FUNCTION z_test_fm.\nENDFUNCTION.\n"),
		})
	if err != nil {
		t.Fatalf("WriteSource returned a transport error instead of a logical failure: %v", err)
	}
	if result.Success {
		t.Fatal("FUNC + expected_source_hash must be rejected, not silently accepted")
	}
	if !strings.Contains(result.Message, "not supported for function-module WriteSource") {
		t.Fatalf("unexpected message: %q", result.Message)
	}
}
