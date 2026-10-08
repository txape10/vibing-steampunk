// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_description.go: read and change an object's SE80/SE11 short
// description (adtcore:description) without touching its source.
package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
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
	case action == "read" && t == "param_texts":
		return s.callHandler(ctx, s.handleGetClassParamTexts, paramTextArgs(objectType, objectName, params))
	case action == "edit" && t == "set_param_text":
		return s.callHandler(ctx, s.handleSetClassParamText, paramTextArgs(objectType, objectName, params))
	}
	return nil, false, nil
}

// paramTextArgs builds the arguments of the parameter-text handlers from a
// target such as "CLAS ZCL_X" and the params; params.class overrides the
// target. clear travels as a bool (getStringParam would drop it).
func paramTextArgs(objectType, objectName string, params map[string]any) map[string]any {
	args := map[string]any{}
	if objectType != "" {
		args["object_type"] = objectType
	}
	if objectName != "" {
		args["class"] = objectName
	}
	for _, key := range []string{"object_type", "class", "method", "parameter", "description", "language", "transport"} {
		if v := getStringParam(params, key); v != "" {
			args[key] = v
		}
	}
	if clearText, ok := getBoolParam(params, "clear"); ok {
		args["clear"] = clearText
	}
	return args
}

// paramTextObjectURL is the ADT URL of the class or interface whose texts are
// written, for the package and transport policies. An empty type is a class.
func paramTextObjectURL(objectType, name string) string {
	kind := "classes"
	if objectType == "INTF" {
		kind = "interfaces"
	}
	return "/sap/bc/adt/oo/" + kind + "/" + url.PathEscape(strings.ToLower(name))
}

func (s *Server) handleGetClassParamTexts(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	objectType, err := adt.NormalizeParamTextObjectType(getStringParam(args, "object_type"))
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	if objectType == "" {
		objectType = "CLAS"
	}
	class := getStringParam(args, "class")
	if class == "" {
		return newToolResultError("class is required (target \"CLAS ZCL_X\" or \"INTF ZIF_X\")"), nil
	}
	if errResult := s.ensureWSConnected(ctx, "GetClassParamTexts"); errResult != nil {
		return errResult, nil
	}
	res, err := s.amdpWSClient.GetClassParamTexts(ctx, class, getStringParam(args, "method"), getStringParam(args, "language"), objectType)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetClassParamTexts failed: %v", err)), nil
	}
	return newToolResultJSON(res), nil
}

func (s *Server) handleSetClassParamText(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Nothing below may touch the network before the safety checks.
	if refused := s.refuseUnderSafety(adt.OpUpdate, "SetClassParamText"); refused != nil {
		return refused, nil
	}
	args := request.GetArguments()
	objectType, err := adt.NormalizeParamTextObjectType(getStringParam(args, "object_type"))
	if err != nil {
		return newToolResultError(fmt.Sprintf("SetClassParamText: %v", err)), nil
	}
	// Without a type the object is a class, here and on the SAP side: the
	// package and transport policies below look at that very object.
	if objectType == "" {
		objectType = "CLAS"
	}
	clearText, _ := getBoolParam(args, "clear")
	p := adt.SetClassParamTextParams{
		Class:       getStringParam(args, "class"),
		ObjectType:  objectType,
		Method:      getStringParam(args, "method"),
		Parameter:   getStringParam(args, "parameter"),
		Description: getStringParam(args, "description"),
		Language:    getStringParam(args, "language"),
		Clear:       clearText,
		Transport:   adt.NormalizeTransport(getStringParam(args, "transport")),
	}
	if err := adt.ValidateClassParamTextParams(p); err != nil {
		return newToolResultError(fmt.Sprintf("SetClassParamText: %v", err)), nil
	}
	// The package and transport policies (--allowed-packages,
	// --allow-transportable-edits, --allowed-transports) are enforced here,
	// before the WebSocket is opened. Whether the request exists, is open and
	// is the caller's is checked by SAP. A request is never chosen for the caller.
	if _, err := s.adtClient.PrepareSourceUpdate(ctx, paramTextObjectURL(objectType, p.Class), p.Transport); err != nil {
		return newToolResultError(fmt.Sprintf("SetClassParamText: %v", err)), nil
	}
	if errResult := s.ensureWSConnected(ctx, "SetClassParamText"); errResult != nil {
		return errResult, nil
	}
	res, err := s.amdpWSClient.SetClassParamText(ctx, p)
	if err != nil {
		return newToolResultError(fmt.Sprintf("SetClassParamText failed: %v", err)), nil
	}
	return newToolResultJSON(res), nil
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
