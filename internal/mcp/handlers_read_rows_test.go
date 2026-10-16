package mcp

import "testing"

// all_rows reaches the data preview as the unlimited sentinel, on every route
// into the two row-limited tools. A numeric max_rows of 0 or -1 never survives
// the MCP client, which is why this is a boolean.
func TestAllRows_ReachesDataPreview(t *testing.T) {
	cases := []struct {
		name   string
		args   map[string]any
		wantIn string
	}{
		{"query SQL", map[string]any{"action": "query", "target": "SQL", "params": map[string]any{"sql_query": "SELECT mandt FROM t000", "all_rows": true}}, "rownumber=90000"},
		{"query TABL_CONTENTS", map[string]any{"action": "query", "target": "TABL_CONTENTS T000", "params": map[string]any{"all_rows": true}}, "rownumber=90000"},
		{"read TABL_CONTENTS", map[string]any{"action": "read", "target": "TABL_CONTENTS T000", "params": map[string]any{"all_rows": true}}, "rownumber=90000"},
		{"default stays 100", map[string]any{"action": "query", "target": "SQL", "params": map[string]any{"sql_query": "SELECT mandt FROM t000"}}, "rownumber=100"},
		{"all_rows wins over max_rows", map[string]any{"action": "query", "target": "SQL", "params": map[string]any{"sql_query": "SELECT mandt FROM t000", "max_rows": float64(7), "all_rows": true}}, "rownumber=90000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, rec := newDescTestServer(t)
			_ = callSAP(t, server, tc.args)
			if !rec.hit(tc.wantIn) {
				t.Errorf("no request carried %q: %v", tc.wantIn, rec.calls)
			}
		})
	}
}
