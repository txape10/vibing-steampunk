package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// --- edit MSAG routing (issue #162) ---
//
// Before this fix, SAP(action="edit", target="MSAG ...") had no route at
// all in the hyperfocused tool's edit switch: routeSourceAction's edit block
// only matched CLAS/PROG/INTF/INCL/DDLS/BDEF/SRVD/FUNC/EDITSOURCE, so an
// MSAG edit fell through every router in the chain and came back as a
// generic "no handler found" — even though reading a message class (action
// "read") already worked. These tests exercise the whole SAP(action="edit",
// target="MSAG ...") path against a recording stub server, the same way
// pkg/adt/session_affinity_test.go exercises the ADT layer beneath it.

type msagRecorder struct {
	mu    sync.Mutex
	calls []string // "METHOD path?query"
}

func (r *msagRecorder) handler(route http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.calls = append(r.calls, req.Method+" "+req.URL.Path+"?"+req.URL.RawQuery)
		r.mu.Unlock()

		w.Header().Set("X-CSRF-Token", "TOKEN")
		route(w, req)
	}
}

func (r *msagRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

const msagTestLockXML = `<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><IS_LOCAL>X</IS_LOCAL>
<MODIFICATION_SUPPORT>NoModification</MODIFICATION_SUPPORT>
</DATA></asx:values></asx:abap>`

func newMSAGTestServer(t *testing.T) (*Server, *msagRecorder) {
	t.Helper()
	rec := &msagRecorder{}
	srv := httptest.NewServer(rec.handler(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, msagTestLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := &Config{
		BaseURL:  srv.URL,
		Username: "testuser",
		Password: "testpass",
		Client:   "001",
		Language: "EN",
		Mode:     "hyperfocused", // the "SAP" universal tool only registers in this mode
	}
	server := NewServer(cfg)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	return server, rec
}

func TestEditMSAG_RoutesToMessageClassWriter(t *testing.T) {
	server, rec := newMSAGTestServer(t)

	args := map[string]any{
		"action": "edit",
		"target": "MSAG ZDEMO_MC",
		"params": map[string]any{
			"language": "EN",
			"texts": []map[string]any{
				{"number": "001", "text": "Enter a value"},
			},
		},
	}
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "SAP",
			"arguments": args,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	raw := server.mcpServer.HandleMessage(context.Background(), req)
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	out := string(body)

	if strings.Contains(out, "No handler found") || strings.Contains(out, "no handler") {
		t.Fatalf("edit MSAG has no route (issue #162 regression): %s", out)
	}
	if strings.Contains(out, "unsupported object type") {
		t.Fatalf("edit MSAG fell through to WriteSource's unsupported-type error: %s", out)
	}

	calls := rec.snapshot()
	lockAt, putAt, unlockAt := -1, -1, -1
	for i, c := range calls {
		switch {
		case strings.Contains(c, "_action=LOCK"):
			lockAt = i
		case strings.HasPrefix(c, "PUT ") && strings.Contains(c, "/messageclass/"):
			putAt = i
		case strings.Contains(c, "_action=UNLOCK"):
			unlockAt = i
		}
	}
	if lockAt < 0 || putAt < 0 || unlockAt < 0 || !(lockAt < putAt && putAt < unlockAt) {
		t.Fatalf("expected LOCK -> PUT /messageclass/ -> UNLOCK in order, got: %v (response: %s)", calls, out)
	}
}

func TestEditMSAG_RequiresTextsOrDeleteNumbers(t *testing.T) {
	server, _ := newMSAGTestServer(t)

	args := map[string]any{
		"action": "edit",
		"target": "MSAG ZDEMO_MC",
		"params": map[string]any{
			"language": "EN",
		},
	}
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "SAP",
			"arguments": args,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	raw := server.mcpServer.HandleMessage(context.Background(), req)
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	out := string(body)

	if !strings.Contains(out, "at least one of texts or delete_numbers is required") {
		t.Fatalf("expected a clear error for an empty write, got: %s", out)
	}
}
