package mcp

import (
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

func TestParseReportParams_Empty(t *testing.T) {
	got, err := parseReportParams("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestParseReportParams_FlatObject(t *testing.T) {
	got, err := parseReportParams(`{"P_BUKRS":"1000"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d params, want 1", len(got))
	}
	if got[0].Name != "P_BUKRS" || got[0].Low != "1000" {
		t.Errorf("got %+v, want Name=P_BUKRS Low=1000", got[0])
	}
	if got[0].Kind != "" || got[0].Option != "" {
		t.Errorf("flat-object form should leave Kind/Option unset (saprfc.selectionRows applies the defaults): got %+v", got[0])
	}
}

func TestParseReportParams_Array(t *testing.T) {
	got, err := parseReportParams(`[{"name":"S_WERKS","kind":"S","option":"BT","low":"1000","high":"2000"}]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := saprfc.ReportParam{Name: "S_WERKS", Kind: "S", Option: "BT", Low: "1000", High: "2000"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseReportParams_InvalidJSON(t *testing.T) {
	if _, err := parseReportParams("{not json"); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestParseReportParams_NonStringValueRejected(t *testing.T) {
	_, err := parseReportParams(`{"P_X": 123}`)
	if err == nil {
		t.Fatal("expected an error for a non-string value in the flat-object form")
	}
}

func TestReportWaitSeconds_Default(t *testing.T) {
	req := mcp.CallToolRequest{}
	if got := reportWaitSeconds(req); got != 30*time.Second {
		t.Errorf("got %v, want 30s", got)
	}
}

func TestReportWaitSeconds_CapsAtMax(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"wait_seconds": float64(999)}
	if got := reportWaitSeconds(req); got != 120*time.Second {
		t.Errorf("got %v, want 120s (capped)", got)
	}
}

func TestReportWaitSeconds_Explicit(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"wait_seconds": float64(5)}
	if got := reportWaitSeconds(req); got != 5*time.Second {
		t.Errorf("got %v, want 5s", got)
	}
}

func TestReportWaitSeconds_NegativeIgnored(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"wait_seconds": float64(-1)}
	if got := reportWaitSeconds(req); got != 30*time.Second {
		t.Errorf("got %v, want default 30s for a negative value", got)
	}
}
