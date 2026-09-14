package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDebugUIServesItsPage checks the embed actually carries the page. A
// //go:embed that silently resolves to nothing would build, serve 200, and
// hand the browser an empty document — the failure mode this guards.
func TestDebugUIServesItsPage(t *testing.T) {
	srv := &debugUIServer{}
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"<title>vsp debugger</title>", "/api/listen", "/api/run/report", "/api/run/rfc", "Call stack"} {
		if !strings.Contains(body, want) {
			t.Errorf("the served page is missing %q — is the embed resolving?", want)
		}
	}
}

// TestDebugUIUnknownPathIs404 pins that the index handler does not answer for
// every path, which would swallow a mistyped API route into an HTML page and
// make the JSON decode fail somewhere far away.
func TestDebugUIUnknownPathIs404(t *testing.T) {
	srv := &debugUIServer{}
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/typo", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/typo = %d, want 404", rec.Code)
	}
}

// TestDebugUIStateWhenDetached is the state the page opens in, and it must be
// a well-formed answer rather than an error: nothing is attached yet, and
// saying so is the correct response, not a failure.
func TestDebugUIStateWhenDetached(t *testing.T) {
	srv := &debugUIServer{}
	rec := httptest.NewRecorder()
	srv.handleState(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/state", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("state = %d, want 200", rec.Code)
	}
	var st uiState
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("state is not valid JSON: %v", err)
	}
	if st.Attached {
		t.Error("a fresh server must not report itself attached")
	}
	if st.Note == "" {
		t.Error("a detached state must explain itself; the page shows this note verbatim")
	}
}

// TestDebugUIRejectsAnUnsupportedStep guards the switch that keeps an
// arbitrary query string from reaching the ADT debugger as a step type.
// terminateDebuggee is a real DebugStepType and precisely the one the UI
// must not expose (detach goes through DebuggerDetach, not a raw step).
func TestDebugUIRejectsAnUnsupportedStep(t *testing.T) {
	srv := &debugUIServer{attached: true}
	rec := httptest.NewRecorder()
	srv.handleStep(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/step?type=terminateDebuggee", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("step type terminateDebuggee = %d, want 400 — the UI offers four steps and must not pass others through", rec.Code)
	}
}

// TestDebugUIReadOnlyBlocksBreakpoint pins the bespoke read-only guard: it
// must refuse to set a breakpoint, and it must do so before touching a
// (here nil) wsClient — a nil-dereference panic would be the wrong way to
// fail this.
func TestDebugUIReadOnlyBlocksBreakpoint(t *testing.T) {
	srv := &debugUIServer{readOnly: true}
	rec := httptest.NewRecorder()
	srv.handleBreakpoint(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/bp?object=ZDEMO&line=10", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-only /api/bp = %d, want 403", rec.Code)
	}
}

// TestDebugUIReadOnlyAllowsListen pins the other half of the guard: Listen
// is passive (nothing is triggered, nothing is written), so read-only mode
// must not block it — only tryStart's busy-session check should be able to,
// and a fresh server is never busy.
func TestDebugUIReadOnlyAllowsListen(t *testing.T) {
	srv := &debugUIServer{readOnly: true}
	if !srv.tryStart() {
		t.Fatal("a fresh, read-only server refused to start a listen — read-only must not gate Listen")
	}
	srv.finish()
}

// TestDebugUITryStartRefusesConcurrentSessions pins tryStart/finish, the
// mechanism every Run/Listen handler uses to serialize itself: two
// overlapping calls on one session would race the same SAP debug session
// against itself.
func TestDebugUITryStartRefusesConcurrentSessions(t *testing.T) {
	srv := &debugUIServer{}
	if !srv.tryStart() {
		t.Fatal("first tryStart should succeed")
	}
	if srv.tryStart() {
		t.Fatal("a second concurrent tryStart should be refused while the first is still in flight")
	}
	srv.finish()
	if !srv.tryStart() {
		t.Fatal("tryStart should succeed again after finish")
	}
	srv.finish()
}

// TestDebugUIBreakpointRequiresAnObject pins that an empty object name is
// reported back as a note rather than reaching the (here nil) wsClient.
func TestDebugUIBreakpointRequiresAnObject(t *testing.T) {
	srv := &debugUIServer{}
	rec := httptest.NewRecorder()
	srv.handleBreakpoint(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/bp?line=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("missing object = %d, want 200 with a note", rec.Code)
	}
	var st uiState
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if !strings.Contains(st.Note, "name an object") {
		t.Errorf("note = %q, want it to say an object is required", st.Note)
	}
}
