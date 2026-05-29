package mcp

import (
	"context"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/graph"
)

// NewServerForCLI creates a minimal Server wrapping an existing ADT client.
// Used by CLI commands that need to call handler logic directly without starting
// a full MCP server (no tool registration, no WebSocket, no async tasks).
func NewServerForCLI(client *adt.Client) *Server {
	return &Server{
		adtClient:  client,
		asyncTasks: make(map[string]*AsyncTask),
	}
}

// RunHardcodeUsage is the exported entry point for the hardcode-usage CLI command.
// It fetches ZTCA_HARDCODE entries filtered by field and/or program, then enriches
// them with caller analysis. When doGrep is false, confidence stays at MEDIUM.
func (s *Server) RunHardcodeUsage(ctx context.Context, field, program string, doGrep bool) (*graph.HardcodeUsageResult, error) {
	entries, filterLabel, err := s.fetchHardcodeEntries(ctx, field, program)
	if err != nil {
		return nil, err
	}

	globalCallers, err := s.fetchHardcodeGlobalCallers(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.enrichHardcodeEntries(ctx, entries, globalCallers, doGrep); err != nil {
		return nil, err
	}

	return &graph.HardcodeUsageResult{
		Filter:  filterLabel,
		Found:   len(entries) > 0,
		Entries: entries,
	}, nil
}

// RunHardcodeAudit is the exported entry point for the hardcode-audit CLI command.
// It reads all ZTCA_HARDCODE entries and produces a full audit report.
func (s *Server) RunHardcodeAudit(ctx context.Context, doGrep bool) (*graph.HardcodeAuditResult, error) {
	entries, _, err := s.fetchHardcodeEntries(ctx, "", "")
	if err != nil {
		return nil, err
	}

	globalCallers, err := s.fetchHardcodeGlobalCallers(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.enrichHardcodeEntries(ctx, entries, globalCallers, doGrep); err != nil {
		return nil, err
	}

	return buildHardcodeAuditResult(entries), nil
}
