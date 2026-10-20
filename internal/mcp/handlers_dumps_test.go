package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const dumpRouteFormatted = `----------------------------------------------------------------------------------------------------
Category               ABAP programming error
Runtime Errors         MESSAGE_TYPE_X
ABAP: Program          ZDEMO_REPORT
----------------------------------------------------------------------------------------------------

----------------------------------------------------------------------------------------------------
|Short Text                                                                                        |
|    The current application has intentionally triggered a termination.                            |
----------------------------------------------------------------------------------------------------
`

func newDumpRouteServer(t *testing.T, mutate func(*Config)) (*Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if r.Method != http.MethodGet || strings.Contains(r.URL.Path, "/discovery") {
			return
		}
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/formatted") {
			_, _ = w.Write([]byte(dumpRouteFormatted))
			return
		}
		_, _ = w.Write([]byte("<html><head><title>Viewer</title></head><body>viewer</body></html>"))
	}))
	t.Cleanup(srv.Close)
	cfg := &Config{BaseURL: srv.URL, Username: "testuser", Password: "testpass", Client: "001", Language: "ES", Mode: "hyperfocused"}
	if mutate != nil {
		mutate(cfg)
	}
	server := NewServer(cfg)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	return server, &paths
}

func TestGetDumpRoute_DetailByDefaultNoHTML(t *testing.T) {
	// A read: it must work under read-only, which is how production runs.
	server, paths := newDumpRouteServer(t, readOnly)
	out := callSAP(t, server, map[string]any{"action": "analyze", "params": map[string]any{"type": "get_dump", "dump_id": "ID1"}})

	if !strings.Contains(out, `\"detail\"`) || !strings.Contains(out, "MESSAGE_TYPE_X") || !strings.Contains(out, "intentionally triggered") {
		t.Errorf("no explained detail in: %s", out)
	}
	if strings.Contains(out, "rawHtml") || len(*paths) != 1 {
		t.Errorf("the viewer HTML was fetched or returned by default (paths %v)", *paths)
	}

	out = callSAP(t, server, map[string]any{"action": "analyze", "params": map[string]any{"type": "get_dump", "dump_id": "ID1", "include_html": true}})
	if !strings.Contains(out, "rawHtml") || !strings.Contains(out, "viewer") {
		t.Errorf("include_html did not return the document: %s", out)
	}
}
