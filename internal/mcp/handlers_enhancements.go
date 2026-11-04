package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// routeEnhancementsAction routes "analyze" with the BAdI types.
func (s *Server) routeEnhancementsAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action != "analyze" {
		return nil, false, nil
	}
	if getStringParam(params, "type") == "badi_implementations" {
		return s.callHandler(ctx, s.handleBadiImplementations, params)
	}
	return nil, false, nil
}

type badiImplementationsResult struct {
	Badi            string                   `json:"badi"`
	Implementations []adt.BadiImplementation `json:"implementations"`
	Count           int                      `json:"count"`
	Note            string                   `json:"note,omitempty"`
}

func (s *Server) handleBadiImplementations(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	badi := firstStringParam(request.GetArguments(), "badi", "badi_name", "name")
	if badi == "" {
		return newToolResultError("badi_implementations needs badi (the BAdI definition name, e.g. ME_PROCESS_PO_CUST)"), nil
	}
	impls, note, err := s.adtClient.BadiImplementations(ctx, badi)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to list the implementations of %s: %v", badi, err)), nil
	}
	return newToolResultJSON(badiImplementationsResult{Badi: badi, Implementations: impls, Count: len(impls), Note: note}), nil
}
