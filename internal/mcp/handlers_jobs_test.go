package mcp

import (
	"strings"
	"testing"
)

func TestJobDateParam(t *testing.T) {
	for _, in := range []string{"2026-09-01", "20260901"} {
		got, err := jobDateParam(map[string]any{"since": in}, "from", "since")
		if err != nil || got.Format("2006-01-02") != "2026-09-01" {
			t.Errorf("%q: %v, %v", in, got, err)
		}
	}
	if got, err := jobDateParam(map[string]any{}, "from"); err != nil || !got.IsZero() {
		t.Errorf("absent: %v, %v", got, err)
	}
	for _, in := range []string{"01.09.2026", "2026-13-40", "x"} {
		if _, err := jobDateParam(map[string]any{"from": in}, "from"); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestFirstParams(t *testing.T) {
	args := map[string]any{"job": " ZX ", "top": float64(5)}
	if got := firstStringParam(args, "name", "job"); got != "ZX" {
		t.Errorf("got %q", got)
	}
	if n, ok := firstFloatParam(args, "max_results", "top"); !ok || n != 5 {
		t.Errorf("got %v %v", n, ok)
	}
}

func TestJobLog_ReadOnlyRefusedBeforeConnecting(t *testing.T) {
	server, rc, gw := newReadOnlyTestServer(t, readOnly)
	out := callSAP(t, server, map[string]any{"action": "analyze", "params": map[string]any{"type": "job_log", "job": "ZDEMO", "count": "09091200"}})
	assertBlocked(t, out)
	if gw.settled() != 0 || rc.count() != 0 {
		t.Errorf("a refused job_log reached SAP: gateway=%d adt=%d", gw.settled(), rc.count())
	}
}

func TestJobLog_RejectsBadInputBeforeAnyConnection(t *testing.T) {
	for _, p := range []map[string]any{
		{"type": "job_log"},
		{"type": "job_log", "job": "X' OR 1=1", "count": "09091200"},
		{"type": "job_log", "job": "ZDEMO", "count": "1' OR '1"},
	} {
		server, rc, gw := newReadOnlyTestServer(t, nil)
		out := callSAP(t, server, map[string]any{"action": "analyze", "params": p})
		if !strings.Contains(out, "job_log needs") && !strings.Contains(out, "invalid job") {
			t.Errorf("%v: %s", p, out)
		}
		if gw.settled() != 0 || rc.count() != 0 {
			t.Errorf("%v reached SAP", p)
		}
	}
}

func TestSpoolRead_RFCReadOnlyRefusedAndBadVia(t *testing.T) {
	server, _, gw := newReadOnlyTestServer(t, readOnly)
	out := callSAP(t, server, map[string]any{"action": "analyze", "params": map[string]any{"type": "spool_read", "number": 1, "via": "rfc"}})
	assertBlocked(t, out)
	if gw.settled() != 0 {
		t.Error("refused spool_read reached the gateway")
	}
	out = callSAP(t, server, map[string]any{"action": "analyze", "params": map[string]any{"type": "spool_read", "number": 1, "via": "x"}})
	if !strings.Contains(out, "not valid") {
		t.Errorf("via x: %s", out)
	}
}
