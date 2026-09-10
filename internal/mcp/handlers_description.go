// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_description.go: read and change an object's SE80/SE11 short
// description (adtcore:description) without touching its source.
package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// routeDescriptionAction routes the description ops on the universal tool:
//
//	SAP(action="read", target="PROG ZDEMO", params={"type": "description"})
//	SAP(action="edit", target="PROG ZDEMO", params={"type": "set_description", "description": "..."})
//
// It is registered first in the universal route chain: routeSourceAction's
// read branch calls handleGetSource unconditionally for PROG/CLAS/… and
// would otherwise shadow a description read. The guards here are strict
// (exact action + params.type), so nothing else is affected — every other
// call falls straight through.
func (s *Server) routeDescriptionAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	t := getStringParam(params, "type")
	switch {
	case action == "read" && t == "description":
		return s.callHandler(ctx, s.handleGetDescription, descriptionArgs(objectType, objectName, params))
	case action == "edit" && t == "set_description":
		args := descriptionArgs(objectType, objectName, params)
		if v := getStringParam(params, "description"); v != "" {
			args["description"] = v
		}
		if v := getStringParam(params, "transport"); v != "" {
			args["transport"] = v
		}
		return s.callHandler(ctx, s.handleSetDescription, args)
	}
	return nil, false, nil
}

// descriptionArgs fills object_type / name / parent from a target such as
// "PROG ZDEMO" or "FUNC Z_FM" (+ params.parent), with params overriding.
func descriptionArgs(objectType, objectName string, params map[string]any) map[string]any {
	args := map[string]any{}
	if objectType != "" {
		args["object_type"] = objectType
	}
	if objectName != "" {
		args["name"] = objectName
	}
	if v := getStringParam(params, "object_type"); v != "" {
		args["object_type"] = v
	}
	if v := getStringParam(params, "name"); v != "" {
		args["name"] = v
	}
	if v := getStringParam(params, "parent"); v != "" {
		args["parent"] = v
	}
	return args
}

func (s *Server) handleGetDescription(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	name := getStringParam(args, "name")
	if name == "" {
		return newToolResultError("name is required"), nil
	}
	res, err := s.adtClient.GetDescription(ctx, getStringParam(args, "object_type"), name, getStringParam(args, "parent"))
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetDescription failed: %v", err)), nil
	}
	return newToolResultJSON(res), nil
}

func (s *Server) handleSetDescription(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	name := getStringParam(args, "name")
	if name == "" {
		return newToolResultError("name is required"), nil
	}
	description := getStringParam(args, "description")
	if description == "" {
		return newToolResultError("description is required (to clear a description, use SE80/SE11)"), nil
	}
	res, err := s.adtClient.SetDescription(ctx,
		getStringParam(args, "object_type"), name, getStringParam(args, "parent"),
		description, getStringParam(args, "transport"))
	if err != nil {
		return newToolResultError(fmt.Sprintf("SetDescription failed: %v", err)), nil
	}
	return newToolResultJSON(res), nil
}
