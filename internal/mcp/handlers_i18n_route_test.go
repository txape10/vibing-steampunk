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

// --- action="i18n" routing (the revived op-keyed translation surface) ---
//
// The individual i18n tools only register in expert/focused mode; the mode
// that ships is hyperfocused, whose single "SAP" tool reaches them through
// routeI18nAction. Before it was revived, an agent in hyperfocused mode
// could not read or write a text pool at all.

type i18nRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *i18nRecorder) handler(route http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.calls = append(r.calls, req.Method+" "+req.URL.Path)
		r.mu.Unlock()
		w.Header().Set("X-CSRF-Token", "TOKEN")
		route(w, req)
	}
}

func newI18nTestServer(t *testing.T) (*Server, *i18nRecorder) {
	t.Helper()
	rec := &i18nRecorder{}
	srv := httptest.NewServer(rec.handler(func(w http.ResponseWriter, r *http.Request) {
		path := strings.ToLower(r.URL.Path)
		switch {
		case strings.HasSuffix(path, "/textelements/programs/zdemo_run/source/symbols"):
			_, _ = io.WriteString(w, "@MaxLength:40\n001=Nothing found\n")
		case strings.Contains(path, "/textelements/programs/"):
			// selections / headings — no selection screen.
			w.WriteHeader(http.StatusNotFound)
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
		Mode:     "hyperfocused",
	}
	server := NewServer(cfg)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	return server, rec
}

func callSAP(t *testing.T, server *Server, args map[string]any) string {
	t.Helper()
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "SAP", "arguments": args},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	raw := server.mcpServer.HandleMessage(context.Background(), req)
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return string(body)
}

func TestI18nRoute_TextsGet(t *testing.T) {
	server, rec := newI18nTestServer(t)
	out := callSAP(t, server, map[string]any{
		"action": "i18n",
		"params": map[string]any{"op": "texts_get", "program_name": "ZDEMO_RUN"},
	})
	if strings.Contains(out, "No handler found") || strings.Contains(out, "does not exist") {
		t.Fatalf("action=i18n op=texts_get did not route: %s", out)
	}
	if !strings.Contains(out, "Nothing found") {
		t.Fatalf("expected the text pool entry in the result: %s", out)
	}
	var sawSymbols bool
	for _, c := range rec.calls {
		if strings.Contains(c, "/source/symbols") {
			sawSymbols = true
		}
	}
	if !sawSymbols {
		t.Errorf("the native REST text-pool resource was not hit: %v", rec.calls)
	}
}

func TestI18nRoute_UnknownOpExplains(t *testing.T) {
	server, _ := newI18nTestServer(t)
	out := callSAP(t, server, map[string]any{
		"action": "i18n",
		"params": map[string]any{"op": "nonsense"},
	})
	if !strings.Contains(out, "params.op") || !strings.Contains(out, "texts_get") {
		t.Fatalf("an unknown i18n op should list the valid ops, got: %s", out)
	}
}
