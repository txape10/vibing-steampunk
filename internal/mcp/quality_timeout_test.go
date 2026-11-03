package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ATC and coverage runs go through the call budget: a run slower than
// params.timeout ends with the explained timeout, not a bare context error.
func TestQualityCallsHonourParamsTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "T")
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer ts.Close()
	s := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN"})
	for _, c := range []struct {
		op   string
		call func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{"ATC run", s.handleRunATCCheck},
		{"ABAP Unit coverage run", s.handleGetCodeCoverage},
	} {
		res, _ := c.call(context.Background(), longCallReq(map[string]any{
			"object_url": "/sap/bc/adt/oo/classes/zcl_demo", "timeout": 0.2,
		}))
		if res == nil || !strings.Contains(callResultText(res), c.op+" timed out after") {
			t.Errorf("%s: got %v", c.op, callResultText(res))
		}
	}
}
