package mcp

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --read-only (and the package whitelist) for the handlers that do not go
// through pkg/adt's own gates: they talk to an RFC gateway or to the ZADT_VSP
// WebSocket. A refused call must reach neither, and every refusal test has a
// control proving the same call does connect once the gate lets it through.

// fakeGateway is a TCP listener standing in for the SAP gateway. It only
// counts connections; the RFC logon that follows fails, which is fine — the
// tests care whether a socket was opened at all.
type fakeGateway struct {
	host  string
	port  int
	conns atomic.Int32
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	g := &fakeGateway{host: "127.0.0.1", port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			g.conns.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return g
}

// waitForConns gives the accept loop a moment: the connection is counted on
// the listener's goroutine, not on the caller's.
func (g *fakeGateway) waitForConns(min int32) int32 {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := g.conns.Load(); n >= min {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
	return g.conns.Load()
}

// settled returns the connection count after a short pause, for the refusal
// tests that must not wait out waitForConns' full deadline for a socket that
// is never going to arrive.
func (g *fakeGateway) settled() int32 {
	time.Sleep(250 * time.Millisecond)
	return g.conns.Load()
}

// requestCounter is an ADT/WebSocket stand-in that counts what reaches it.
type requestCounter struct {
	mu    sync.Mutex
	paths []string
}

func (c *requestCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths)
}

func newReadOnlyTestServer(t *testing.T, mutate func(*Config)) (*Server, *requestCounter, *fakeGateway) {
	t.Helper()
	rc := &requestCounter{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.mu.Lock()
		rc.paths = append(rc.paths, r.Method+" "+r.URL.Path)
		rc.mu.Unlock()
		w.Header().Set("X-CSRF-Token", "TOKEN")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	gw := newFakeGateway(t)
	cfg := &Config{
		BaseURL:  srv.URL,
		Username: "testuser",
		Password: "testpass",
		Client:   "001",
		Language: "EN",
		Mode:     "hyperfocused",
		RFCHost:  gw.host,
		RFCSysnr: "00",
		RFCPort:  gw.port,
	}
	if mutate != nil {
		mutate(cfg)
	}
	server := NewServer(cfg)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	return server, rc, gw
}

func readOnly(c *Config) { c.ReadOnly = true }

func assertBlocked(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "blocked by safety configuration") {
		t.Errorf("expected a safety refusal, got: %s", out)
	}
}

// The gated RFC routes — one table, each refused with no socket opened.
var readOnlyRFCRoutes = map[string]map[string]any{
	"CALL_RFC":         {"function": "RFC_PING"},
	"RUN_REPORT":       {"report": "ZDEMO"},
	"RUN_REPORT_ASYNC": {"report": "ZDEMO"},
}

func TestReadOnly_RFCRoutesRefusedBeforeConnecting(t *testing.T) {
	for target, params := range readOnlyRFCRoutes {
		t.Run(target, func(t *testing.T) {
			server, rc, gw := newReadOnlyTestServer(t, readOnly)
			out := callSAP(t, server, map[string]any{"action": "debug", "target": target, "params": params})
			assertBlocked(t, out)
			if n := gw.settled(); n != 0 {
				t.Errorf("%s was refused but %d connection(s) reached the gateway", target, n)
			}
			if n := rc.count(); n != 0 {
				t.Errorf("%s was refused but %d ADT request(s) were sent", target, n)
			}
		})
	}
}

func TestReadOnly_RFCRoutesConnectWithoutReadOnly(t *testing.T) {
	for target, params := range readOnlyRFCRoutes {
		t.Run(target, func(t *testing.T) {
			server, _, gw := newReadOnlyTestServer(t, nil)
			out := callSAP(t, server, map[string]any{"action": "debug", "target": target, "params": params})
			if strings.Contains(out, "blocked by safety configuration") {
				t.Fatalf("without --read-only %s must not be refused: %s", target, out)
			}
			if n := gw.waitForConns(1); n == 0 {
				t.Errorf("without --read-only %s should open a gateway connection, none seen", target)
			}
		})
	}
}

// The read-only RFC operations keep working under --read-only.
func TestReadOnly_RFCReadsStillConnect(t *testing.T) {
	reads := map[string]map[string]any{
		"RFC_SEARCH":            {"pattern": "RFC_PING*"},
		"RFC_METADATA":          {"function": "RFC_PING"},
		"GET_REPORT_JOB_STATUS": {"job_name": "VSP_ZDEMO", "job_count": "12345678"},
	}
	for target, params := range reads {
		t.Run(target, func(t *testing.T) {
			server, _, gw := newReadOnlyTestServer(t, readOnly)
			out := callSAP(t, server, map[string]any{"action": "debug", "target": target, "params": params})
			if strings.Contains(out, "blocked by safety configuration") {
				t.Fatalf("%s is a read and must not be refused under --read-only: %s", target, out)
			}
			if n := gw.waitForConns(1); n == 0 {
				t.Errorf("%s should reach the gateway under --read-only, no connection seen", target)
			}
		})
	}
}

// The RunReportAsync gate sits before the task is registered, so a refusal
// leaves no task behind for GET_ASYNC_RESULT to report on.
func TestReadOnly_RunReportAsyncRegistersNoTask(t *testing.T) {
	server, _, _ := newReadOnlyTestServer(t, readOnly)
	_ = callSAP(t, server, map[string]any{"action": "debug", "target": "RUN_REPORT_ASYNC", "params": map[string]any{"report": "ZDEMO"}})
	server.asyncTasksMu.Lock()
	defer server.asyncTasksMu.Unlock()
	if n := len(server.asyncTasks); n != 0 {
		t.Errorf("a refused RunReportAsync registered %d task(s)", n)
	}
}

// The WebSocket routes: nothing may reach the ADT host (the WS upgrade is an
// HTTP request to it) when refused.
func TestReadOnly_WebSocketRoutesRefusedBeforeConnecting(t *testing.T) {
	routes := map[string]map[string]any{
		"debug SET_TEXT_ELEMENTS": {"action": "debug", "target": "SET_TEXT_ELEMENTS",
			"params": map[string]any{"program": "ZDEMO", "selection_texts": `{"P_X":"x"}`}},
		"debug MOVE": {"action": "debug", "target": "MOVE",
			"params": map[string]any{"object_type": "PROG", "object_name": "ZDEMO", "new_package": "ZOTHER"}},
		"edit MOVE": {"action": "edit", "target": "MOVE",
			"params": map[string]any{"object_type": "PROG", "object_name": "ZDEMO", "new_package": "ZOTHER"}},
	}
	for name, args := range routes {
		t.Run(name, func(t *testing.T) {
			server, rc, _ := newReadOnlyTestServer(t, readOnly)
			before := rc.count()
			out := callSAP(t, server, args)
			assertBlocked(t, out)
			if n := rc.count() - before; n != 0 {
				t.Errorf("%s was refused but %d request(s) reached the ADT host", name, n)
			}
		})
	}
}

// Control for the WebSocket routes: a read of the same domain does connect.
func TestReadOnly_GetTextElementsStillConnects(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, readOnly)
	before := rc.count()
	out := callSAP(t, server, map[string]any{"action": "debug", "target": "GET_TEXT_ELEMENTS",
		"params": map[string]any{"program": "ZDEMO"}})
	if strings.Contains(out, "blocked by safety configuration") {
		t.Fatalf("GET_TEXT_ELEMENTS is a read and must not be refused: %s", out)
	}
	if rc.count() == before {
		t.Error("GET_TEXT_ELEMENTS should reach the ADT host under --read-only")
	}
}

// MoveObject checks the destination against AllowedPackages (Z*,$TMP here),
// independent of --read-only.
func TestMoveObject_DestinationPackageHonoursAllowedPackages(t *testing.T) {
	allow := func(c *Config) { c.AllowedPackages = []string{"Z*", "$TMP"} }
	args := func(pkg string) map[string]any {
		return map[string]any{"action": "debug", "target": "MOVE",
			"params": map[string]any{"object_type": "PROG", "object_name": "ZDEMO", "new_package": pkg}}
	}

	server, rc, _ := newReadOnlyTestServer(t, allow)
	before := rc.count()
	out := callSAP(t, server, args("SAP_STANDARD_PKG"))
	assertBlocked(t, out)
	if n := rc.count() - before; n != 0 {
		t.Errorf("a move into a package outside the whitelist sent %d request(s)", n)
	}

	// Control: a package inside the whitelist is let through to the connection
	server, rc, _ = newReadOnlyTestServer(t, allow)
	before = rc.count()
	out = callSAP(t, server, args("ZOTHER"))
	if strings.Contains(out, "blocked by safety configuration") {
		t.Fatalf("a move into an allowed package must not be refused: %s", out)
	}
	if rc.count() == before {
		t.Error("a move into an allowed package should reach the ADT host")
	}
}

// edit LOCK goes through pkg/adt's LockObject; the MCP route is covered too.
func TestReadOnly_EditLockRefused(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, readOnly)
	before := rc.count()
	out := callSAP(t, server, map[string]any{"action": "edit", "target": "LOCK",
		"params": map[string]any{"object_url": "/sap/bc/adt/programs/programs/zdemo"}})
	if !strings.Contains(out, "read-only") {
		t.Errorf("expected a read-only refusal, got: %s", out)
	}
	if n := rc.count() - before; n != 0 {
		t.Errorf("a refused LOCK sent %d request(s)", n)
	}
}
