package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func callDeployZip(t *testing.T, s *Server, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleDeployZip(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDeployZip: %v", err)
	}
	return res
}

// A transportable package needs a request the caller names: SAP must never
// generate one, and nothing may reach SAP before that is settled.
func TestDeployZip_TransportablePackageRequiresTransport(t *testing.T) {
	s, rc, _ := newReadOnlyTestServer(t, nil)
	res := callDeployZip(t, s, map[string]any{"source": "abapgit-standalone", "package": "ZDEMO_PKG"})
	if !res.IsError || !strings.Contains(resultText(res), "transport is required") {
		t.Fatalf("expected a transport-required refusal, got: %s", resultText(res))
	}
	if rc.count() != 0 {
		t.Errorf("%d request(s) reached SAP before the transport was settled", rc.count())
	}
}

func TestDeployZip_LocalPackageRejectsTransport(t *testing.T) {
	s, rc, _ := newReadOnlyTestServer(t, nil)
	res := callDeployZip(t, s, map[string]any{"source": "abapgit-standalone", "package": "$ZGIT", "transport": "A4HK000001"})
	if !res.IsError || !strings.Contains(resultText(res), "not allowed for a local package") {
		t.Fatalf("expected a refusal of transport on a local package, got: %s", resultText(res))
	}
	if rc.count() != 0 {
		t.Errorf("%d request(s) reached SAP", rc.count())
	}
}

// A dry run changes nothing, so it needs neither a transport nor write rights.
func TestDeployZip_DryRunNeedsNoTransport(t *testing.T) {
	s, rc, _ := newReadOnlyTestServer(t, readOnly)
	res := callDeployZip(t, s, map[string]any{"source": "abapgit-standalone", "package": "ZDEMO_PKG", "dry_run": true})
	text := resultText(res)
	if strings.Contains(text, "transport is required") || strings.Contains(text, "blocked by safety configuration") {
		t.Fatalf("dry run was refused: %s", text)
	}
	if rc.count() != 0 {
		t.Errorf("dry run sent %d request(s)", rc.count())
	}
}
