package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestCallBudget(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		def     time.Duration
		want    time.Duration
		wantErr bool
	}{
		{"none", map[string]any{}, 0, 0, false},
		{"server default", map[string]any{}, 90 * time.Second, 90 * time.Second, false},
		{"param wins", map[string]any{"timeout": 10.0}, 90 * time.Second, 10 * time.Second, false},
		{"string", map[string]any{"timeout": "7"}, 0, 7 * time.Second, false},
		{"zero", map[string]any{"timeout": 0.0}, 0, 0, true},
		{"negative", map[string]any{"timeout": -3.0}, 0, 0, true},
		{"junk", map[string]any{"timeout": "abc"}, 0, 0, true},
		{"cap", map[string]any{"timeout": 1e12}, 0, MaxCallTimeout, false},
		{"tiny", map[string]any{"timeout": 1e-12}, 0, 0, true},
	}
	for _, c := range cases {
		got, err := callBudget(c.args, c.def)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%s: got %v, %v", c.name, got, err)
		}
	}
}

func longCallReq(args map[string]any) mcp.CallToolRequest {
	r := mcp.CallToolRequest{}
	r.Params.Arguments = args
	return r
}

func TestLongCall_TimeoutIsExplained(t *testing.T) {
	s := &Server{config: &Config{}}
	res, err := s.longCall(context.Background(), longCallReq(map[string]any{"timeout": 0.2}), "ExecuteABAP",
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			<-ctx.Done()
			return newToolResultError("run failed: " + ctx.Err().Error()), nil
		})
	if err != nil || !res.IsError {
		t.Fatalf("got %v %v", res, err)
	}
	txt := callResultText(res)
	if !strings.Contains(txt, "ExecuteABAP timed out after") || !strings.Contains(txt, "may still be running") {
		t.Fatalf("message: %s", txt)
	}
}

func TestLongCall_InvalidTimeoutRunsNothing(t *testing.T) {
	s := &Server{config: &Config{}}
	ran := false
	res, _ := s.longCall(context.Background(), longCallReq(map[string]any{"timeout": -1.0}), "RunUnitTests",
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) { ran = true; return nil, nil })
	if ran || !res.IsError {
		t.Fatal("an invalid timeout must be refused before the handler runs")
	}
}

func TestLongCall_OrdinaryResultUntouched(t *testing.T) {
	s := &Server{config: &Config{CallTimeout: time.Minute}}
	res, _ := s.longCall(context.Background(), longCallReq(nil), "DeployFromFile",
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("done"), nil
		})
	if callResultText(res) != "done" {
		t.Fatalf("got %q", callResultText(res))
	}
}

// End to end: a request slower than the 60s-style per-request limit completes
// under params.timeout and is cut off by it when it is too short.
func TestLongCall_ThroughExecuteABAPTool(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("X-CSRF-Token", "T")
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	s := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN"})
	res, _ := s.handleExecuteABAP(context.Background(), longCallReq(map[string]any{"code": "WRITE 1.", "timeout": 0.05}))
	if res == nil || !strings.Contains(callResultText(res), "ExecuteABAP timed out after") {
		t.Fatalf("want a timeout explanation, got %v", res)
	}
}

// A result that merely mentions a marker, with no budget and a live context,
// is not a timeout of ours and stays as the handler made it.
func TestLongCall_MarkerWithoutBudgetUntouched(t *testing.T) {
	s := &Server{config: &Config{}}
	res, _ := s.longCall(context.Background(), longCallReq(nil), "RunUnitTests",
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("test output: context deadline exceeded in assertion"), nil
		})
	if res.IsError || !strings.Contains(callResultText(res), "test output") {
		t.Fatalf("result was rewritten: %v", callResultText(res))
	}
}

// The caller's own earlier deadline is named as such, and the handler's detail
// (a cleanup warning) survives.
func TestLongCall_CallersEarlierDeadlineKeepsDetail(t *testing.T) {
	s := &Server{config: &Config{}}
	parent, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res, _ := s.longCall(parent, longCallReq(map[string]any{"timeout": 30.0}), "ExecuteABAP",
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			<-ctx.Done()
			return newToolResultError("failed: context deadline exceeded; temp program ZTEMP_X not deleted"), nil
		})
	txt := callResultText(res)
	if !strings.Contains(txt, "caller's own deadline") || !strings.Contains(txt, "ZTEMP_X not deleted") {
		t.Fatalf("got %s", txt)
	}
}
