package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// --- action="read"/"edit" with params.type description / set_description ---
//
// routeDescriptionAction is first in the universal route chain: without it,
// routeSourceAction's read branch would call handleGetSource for PROG and
// shadow a description read.

const descProgDoc = `<?xml version="1.0" encoding="UTF-8"?>
<program:abapProgram xmlns:program="http://www.sap.com/adt/programs/programs" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:description="Old text" adtcore:descriptionTextLimit="70" adtcore:name="ZDEMO_PROG" adtcore:type="PROG/P">
  <adtcore:packageRef adtcore:name="$TMP"/>
</program:abapProgram>`

type descRecorder struct {
	mu    sync.Mutex
	calls []string
}

func newDescTestServer(t *testing.T) (*Server, *descRecorder) {
	t.Helper()
	rec := &descRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.calls = append(rec.calls, strings.ToLower(r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery))
		rec.mu.Unlock()
		w.Header().Set("X-CSRF-Token", "TOKEN")

		path := strings.ToLower(r.URL.Path)
		switch {
		case strings.Contains(path, "/cts/transportchecks"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?><asx:abap xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA><RECORDING></RECORDING><DEVCLASS>$TMP</DEVCLASS></DATA></asx:values></asx:abap>`)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><IS_LOCAL>X</IS_LOCAL>
</DATA></asx:values></asx:abap>`)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "UNLOCK":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.Contains(path, "/activation"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(path, "/programs/programs/zdemo_prog"):
			// GET serves the doc; the read-back after a PUT would too — the
			// stub keeps serving descProgDoc, which is fine for routing tests
			// (the write path's own verification is covered in pkg/adt).
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusOK)
				return
			}
			_, _ = io.WriteString(w, descProgDoc)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := &Config{
		BaseURL: srv.URL, Username: "testuser", Password: "testpass",
		Client: "001", Language: "EN", Mode: "hyperfocused",
	}
	server := NewServer(cfg)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	return server, rec
}

func (r *descRecorder) hit(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestDescriptionRoute_Read(t *testing.T) {
	server, rec := newDescTestServer(t)
	out := callSAP(t, server, map[string]any{
		"action": "read", "target": "PROG ZDEMO_PROG",
		"params": map[string]any{"type": "description"},
	})
	if !strings.Contains(out, "Old text") {
		t.Fatalf("description read did not route / return the text: %s", out)
	}
	if rec.hit("/source/main") {
		t.Errorf("routeSourceAction shadowed the description read (hit /source/main): %v", rec.calls)
	}
	if !rec.hit("get /sap/bc/adt/programs/programs/zdemo_prog") {
		t.Errorf("the object metadata resource was not read: %v", rec.calls)
	}
}

func TestDescriptionRoute_SetDescription(t *testing.T) {
	server, rec := newDescTestServer(t)
	out := callSAP(t, server, map[string]any{
		"action": "edit", "target": "PROG ZDEMO_PROG",
		"params": map[string]any{"type": "set_description", "description": "Brand new"},
	})
	if strings.Contains(out, "does not exist") || strings.Contains(out, "No handler") {
		t.Fatalf("set_description did not route: %s", out)
	}
	if !rec.hit("put /sap/bc/adt/programs/programs/zdemo_prog") {
		t.Errorf("no PUT to the metadata resource: %v", rec.calls)
	}
	if !rec.hit("lock") {
		t.Errorf("no LOCK taken for the change: %v", rec.calls)
	}
}

func TestDescriptionRoute_SetDescriptionNeedsText(t *testing.T) {
	server, _ := newDescTestServer(t)
	out := callSAP(t, server, map[string]any{
		"action": "edit", "target": "PROG ZDEMO_PROG",
		"params": map[string]any{"type": "set_description"},
	})
	if !strings.Contains(out, "description is required") {
		t.Fatalf("expected a 'description is required' error, got: %s", out)
	}
}
