package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// `vsp debug` REPL: run and call execute code on the system. The session's
// client carries the policy resolved for the system (-s/.vsp.json/env), so a
// read-only system must refuse both. The refusal is told apart from the
// session simply having no WebSocket by the error text.

func newDebugSessionFor(opts ...adt.Option) *debugSession {
	// Nothing in these tests may open a connection; an unroutable URL makes a
	// request that slipped through fail loudly instead of reaching anything.
	return &debugSession{client: adt.NewClient("http://127.0.0.1:1", "TESTUSER", "secret", opts...)}
}

func TestDebugREPL_RunAndCallRefusedUnderReadOnly(t *testing.T) {
	s := newDebugSessionFor(adt.WithReadOnly())
	for name, call := range map[string]func() error{
		"run":  func() error { return s.runProgram([]string{"ZDEMO"}) },
		"call": func() error { return s.callRFC([]string{"RFC_PING"}) },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "blocked by safety configuration") {
			t.Errorf("%s under --read-only should be refused by the safety config, got: %v", name, err)
		}
	}
}

func TestDebugREPL_RunAndCallGetPastTheGateWithoutReadOnly(t *testing.T) {
	s := newDebugSessionFor()
	for name, call := range map[string]func() error{
		"run":  func() error { return s.runProgram([]string{"ZDEMO"}) },
		"call": func() error { return s.callRFC([]string{"RFC_PING"}) },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "WebSocket not connected") {
			t.Errorf("%s without --read-only should reach the WebSocket check, got: %v", name, err)
		}
	}
}

// --disallowed-ops W reaches the REPL too; a bespoke read-only boolean would
// not have seen it.
func TestDebugREPL_HonoursDisallowedOps(t *testing.T) {
	s := newDebugSessionFor(adt.WithSafety(adt.SafetyConfig{DisallowedOps: "W"}))
	err := s.runProgram([]string{"ZDEMO"})
	if err == nil || !strings.Contains(err.Error(), "blocked by safety configuration") {
		t.Fatalf("run with W disallowed should be refused, got: %v", err)
	}
}

// debug UI: the Run handlers answer 403 under read-only (the guard existed;
// only the breakpoint path was tested) and under a disallowed W.

func TestDebugUI_RunHandlersRefusedUnderReadOnly(t *testing.T) {
	srv := &debugUIServer{readOnly: true}
	for name, handler := range map[string]http.HandlerFunc{
		"/api/run/report": srv.handleRunReport,
		"/api/run/rfc":    srv.handleRunRFC,
	} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, name+"?object=ZDEMO", nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s under read-only = %d, want 403", name, rec.Code)
		}
	}
}

func TestDebugUI_RunHandlersHonourDisallowedOps(t *testing.T) {
	client := adt.NewClient("http://127.0.0.1:1", "TESTUSER", "secret",
		adt.WithSafety(adt.SafetyConfig{DisallowedOps: "W"}))
	srv := &debugUIServer{client: client}
	for name, handler := range map[string]http.HandlerFunc{
		"/api/run/report": srv.handleRunReport,
		"/api/run/rfc":    srv.handleRunRFC,
	} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, name+"?object=ZDEMO", nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s with W disallowed = %d, want 403", name, rec.Code)
		}
	}
}

func TestDebugUI_RunRefusalEmptyWhenAllowed(t *testing.T) {
	srv := &debugUIServer{client: adt.NewClient("http://127.0.0.1:1", "TESTUSER", "secret")}
	if note := srv.runRefusal("RunReport", "read-only mode"); note != "" {
		t.Errorf("an unrestricted server must not refuse a run, got %q", note)
	}
}
