package mcp

import "testing"

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
