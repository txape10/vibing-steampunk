// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_debugger.go contains handlers for WebSocket-based debugging (via ZADT_VSP).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/open-rfc-go/rfc"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// routeDebuggerAction routes "debug" sub-actions for the WebSocket-based debugger.
func (s *Server) routeDebuggerAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action != "debug" {
		return nil, false, nil
	}
	switch objectType {
	case "SET_BREAKPOINT":
		return s.callHandler(ctx, s.handleSetBreakpoint, params)
	case "GET_BREAKPOINTS":
		return s.callHandler(ctx, s.handleGetBreakpoints, params)
	case "DELETE_BREAKPOINT":
		return s.callHandler(ctx, s.handleDeleteBreakpoint, params)
	case "CALL_RFC":
		return s.callHandler(ctx, s.handleCallRFC, params)
	case "RFC_SEARCH":
		return s.callHandler(ctx, s.handleRFCSearch, params)
	case "RFC_METADATA":
		return s.callHandler(ctx, s.handleRFCGetMetadata, params)
	case "MOVE":
		return s.callHandler(ctx, s.handleMoveObject, params)
	}
	return nil, false, nil
}

// --- Debugger Session Handlers (WebSocket-based via ZADT_VSP) ---
// All breakpoint operations use WebSocket for reliable CSRF-free communication.

// ensureDebugWSClient ensures WebSocket debug client is connected.
func (s *Server) ensureDebugWSClient(ctx context.Context) error {
	if s.debugWSClient != nil && s.debugWSClient.IsConnected() {
		return nil
	}

	// Create new client
	s.debugWSClient = adt.NewDebugWebSocketClient(
		s.config.BaseURL,
		s.config.Client,
		s.config.Username,
		s.config.Password,
		s.config.InsecureSkipVerify,
	)

	return s.debugWSClient.Connect(ctx)
}

func (s *Server) handleSetBreakpoint(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get breakpoint kind (default: "line")
	kind, _ := request.GetArguments()["kind"].(string)
	if kind == "" {
		kind = "line"
	}

	// Ensure WebSocket client is connected
	if err := s.ensureDebugWSClient(ctx); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect to ZADT_VSP WebSocket: %v. Ensure ZADT_VSP is deployed and SAPC/SICF are configured.", err)), nil
	}

	var bpID string
	var err error
	var msg strings.Builder

	switch kind {
	case "line":
		program, ok := request.GetArguments()["program"].(string)
		if !ok || program == "" {
			return newToolResultError("program is required for line breakpoints"), nil
		}

		lineFloat, ok := request.GetArguments()["line"].(float64)
		if !ok || lineFloat <= 0 {
			return newToolResultError("line is required and must be positive for line breakpoints"), nil
		}
		line := int(lineFloat)

		// Optional method parameter for include-relative line numbers
		method, _ := request.GetArguments()["method"].(string)

		// Auto-convert class names to pool format (ZCL_TEST → ZCL_TEST================CP)
		originalProgram := program
		program = convertToClassPool(program)

		// Use method-aware breakpoint if method is specified
		if method != "" {
			bpID, err = s.debugWSClient.SetMethodBreakpoint(ctx, program, method, line)
			if err != nil {
				return newToolResultError(fmt.Sprintf("SetMethodBreakpoint failed: %v", err)), nil
			}

			msg.WriteString("Method breakpoint set successfully!\n\n")
			fmt.Fprintf(&msg, "Breakpoint ID: %s\n", bpID)
			if program != originalProgram {
				fmt.Fprintf(&msg, "Program: %s (converted from %s)\n", program, originalProgram)
			} else {
				fmt.Fprintf(&msg, "Program: %s\n", program)
			}
			fmt.Fprintf(&msg, "Method: %s\n", method)
			fmt.Fprintf(&msg, "Line: %d (relative to method start)\n", line)
			msg.WriteString("\nℹ️  Line number is relative to the METHOD implementation, not the full class.\n")
		} else {
			bpID, err = s.debugWSClient.SetLineBreakpoint(ctx, program, line)
			if err != nil {
				return newToolResultError(fmt.Sprintf("SetLineBreakpoint failed: %v", err)), nil
			}

			msg.WriteString("Line breakpoint set successfully!\n\n")
			fmt.Fprintf(&msg, "Breakpoint ID: %s\n", bpID)
			if program != originalProgram {
				fmt.Fprintf(&msg, "Program: %s (converted from %s)\n", program, originalProgram)
			} else {
				fmt.Fprintf(&msg, "Program: %s\n", program)
			}
			fmt.Fprintf(&msg, "Line: %d (pool-absolute)\n", line)
		}

	case "statement":
		statement, ok := request.GetArguments()["statement"].(string)
		if !ok || statement == "" {
			return newToolResultError("statement is required for statement breakpoints (e.g., 'CALL FUNCTION', 'SELECT', 'LOOP')"), nil
		}

		bpID, err = s.debugWSClient.SetStatementBreakpoint(ctx, statement)
		if err != nil {
			return newToolResultError(fmt.Sprintf("SetStatementBreakpoint failed: %v", err)), nil
		}

		msg.WriteString("Statement breakpoint set successfully!\n\n")
		fmt.Fprintf(&msg, "Breakpoint ID: %s\n", bpID)
		fmt.Fprintf(&msg, "Statement: %s\n", statement)
		msg.WriteString("\nThis breakpoint will trigger on ALL occurrences of this statement type.\n")

	case "exception":
		exception, ok := request.GetArguments()["exception"].(string)
		if !ok || exception == "" {
			return newToolResultError("exception is required for exception breakpoints (e.g., 'CX_SY_ZERODIVIDE')"), nil
		}

		bpID, err = s.debugWSClient.SetExceptionBreakpoint(ctx, exception)
		if err != nil {
			return newToolResultError(fmt.Sprintf("SetExceptionBreakpoint failed: %v", err)), nil
		}

		msg.WriteString("Exception breakpoint set successfully!\n\n")
		fmt.Fprintf(&msg, "Breakpoint ID: %s\n", bpID)
		fmt.Fprintf(&msg, "Exception: %s\n", exception)
		msg.WriteString("\nThis breakpoint will trigger when this exception is raised.\n")

	default:
		return newToolResultError(fmt.Sprintf("Invalid breakpoint kind: %s. Valid kinds: line, statement, exception", kind)), nil
	}

	msg.WriteString("\n⚠️  IMPORTANT: Breakpoints only trigger for code executed in a DIFFERENT SAP session.\n")
	msg.WriteString("Use DebuggerListen in this session, then trigger execution from another session\n")
	msg.WriteString("(e.g., SAP GUI, HTTP request, RunUnitTests from another connection).")

	return mcp.NewToolResultText(msg.String()), nil
}

// convertToClassPool converts class/interface names to pool format for debugging.
// Example: ZCL_TEST → ZCL_TEST================CP (padded to 30 chars + CP suffix)
func convertToClassPool(program string) string {
	program = strings.ToUpper(program)

	// Already in pool format
	if strings.HasSuffix(program, "CP") && strings.Contains(program, "=") {
		return program
	}

	// Check if it looks like a class or interface name
	isClass := strings.HasPrefix(program, "ZCL_") ||
		strings.HasPrefix(program, "YCL_") ||
		strings.HasPrefix(program, "ZIF_") ||
		strings.HasPrefix(program, "YIF_") ||
		strings.HasPrefix(program, "LCL_") ||
		strings.HasPrefix(program, "LIF_") ||
		strings.Contains(program, "/CL_") ||
		strings.Contains(program, "/IF_")

	if !isClass {
		return program
	}

	// Pad to 30 chars with '=' and add 'CP' suffix
	// Total length: 30 + 2 = 32 (standard ABAP class pool naming)
	if len(program) < 30 {
		padding := 30 - len(program)
		program = program + strings.Repeat("=", padding) + "CP"
	}

	return program
}

func (s *Server) handleGetBreakpoints(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.ensureDebugWSClient(ctx); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect to ZADT_VSP WebSocket: %v", err)), nil
	}

	breakpoints, err := s.debugWSClient.GetBreakpoints(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetBreakpoints failed: %v", err)), nil
	}

	if len(breakpoints) == 0 {
		return mcp.NewToolResultText("No breakpoints are currently set."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Active Breakpoints (%d):\n\n", len(breakpoints))
	for i, bp := range breakpoints {
		fmt.Fprintf(&sb, "%d. ID: %v\n", i+1, bp["id"])
		if kind, ok := bp["kind"]; ok {
			fmt.Fprintf(&sb, "   Kind: %v\n", kind)
		}
		if uri, ok := bp["uri"]; ok {
			fmt.Fprintf(&sb, "   URI: %v\n", uri)
		}
		if line, ok := bp["line"]; ok {
			fmt.Fprintf(&sb, "   Line: %v\n", line)
		}
		sb.WriteString("\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleDeleteBreakpoint(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	bpID, ok := request.GetArguments()["breakpoint_id"].(string)
	if !ok || bpID == "" {
		return newToolResultError("breakpoint_id is required"), nil
	}

	if err := s.ensureDebugWSClient(ctx); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect to ZADT_VSP WebSocket: %v", err)), nil
	}

	if err := s.debugWSClient.DeleteBreakpoint(ctx, bpID); err != nil {
		return newToolResultError(fmt.Sprintf("DeleteBreakpoint failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Breakpoint %s deleted successfully.", bpID)), nil
}

// handleCallRFC calls a function module over classic RFC (pkg/saprfc,
// open-rfc-go) — a direct socket connection to the SAP gateway, not the
// ZADT_VSP WebSocket bridge the rest of this file uses. This closes the
// deserialization bug class documented in CLAUDE.md 2al/2y (TABLES parameters
// never reaching ABAP, table-of-tables CREATE DATA failures) at the root: the
// client builds the RFC wire format natively, with no ABAP intermediary to
// have that bug. A genuine ABAP-side failure (declared exception, runtime
// dump, or T100 message) now surfaces as a typed error and sets IsError on
// the tool result — previously every such failure was swallowed into a
// generic "Subrc: 99" inside an otherwise-successful response.
func (s *Server) handleCallRFC(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	function, ok := request.GetArguments()["function"].(string)
	if !ok || function == "" {
		return newToolResultError("function is required"), nil
	}

	params := rfc.Params{}
	if paramsStr, ok := request.GetArguments()["params"].(string); ok && paramsStr != "" {
		// Parse JSON params, preserving structure/array types so nested
		// IMPORTING parameters (e.g. TRACE_INTERVAL) reach ABAP as JSON
		// objects instead of being flattened into Go's "map[...]" string form.
		if err := json.Unmarshal([]byte(paramsStr), &params); err != nil {
			return newToolResultError(fmt.Sprintf("Invalid params JSON: %v", err)), nil
		}
	}

	c, err := s.ensureRFCClient(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect via RFC: %v", err)), nil
	}

	function = strings.ToUpper(strings.TrimSpace(function))
	res, err := c.Call(ctx, function, params)
	if err != nil {
		return newToolResultError(fmt.Sprintf("CallRFC failed: %v", err)), nil
	}

	// Result.MarshalJSON already flattens scalars/structures and tables into
	// one object by export name — no separate Exports/Tables split needed.
	resultJSON, _ := json.MarshalIndent(res, "", "  ")
	return mcp.NewToolResultText(fmt.Sprintf("RFC call completed.\n\nFunction: %s\nSubrc: 0\n\nResult:\n%s", function, string(resultJSON))), nil
}

// funcnameLikePredicate turns a user-supplied function-module name pattern
// ('*' as wildcard) into a SQL LIKE operand safe to interpolate into an
// RFC_READ_TABLE OPTIONS WHERE clause. pkg/saprfc.ReadTable has no bind
// parameters — the whole clause is a literal string sent over RFC — so an
// unescaped pattern is a WHERE-clause injection into a live SAP system, the
// same class of risk this project already closed for SAP user names in
// as4userPredicate (pkg/adt/transport.go). A function module name is
// letters, digits, underscore, and '/' for a namespace (e.g.
// /NAMESPACE/FUNC); anything else — starting with a single quote — is
// rejected outright rather than escaped.
func funcnameLikePredicate(pattern string) (string, error) {
	name := strings.ToUpper(strings.TrimSpace(pattern))
	if name == "" {
		return "%", nil
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '/', r == '*':
		default:
			return "", fmt.Errorf("invalid function module pattern %q: expected letters, digits, "+
				"_ / and '*' as a wildcard", pattern)
		}
	}
	like := strings.ReplaceAll(name, "*", "%")
	if !strings.Contains(like, "%") {
		like = "%" + like + "%"
	}
	return like, nil
}

// handleRFCSearch finds RFC-enabled function modules by name pattern, reading
// TFDIR directly over RFC_READ_TABLE (pkg/saprfc.ReadTable) instead of the
// ZADT_VSP WebSocket bridge. FMODE IN ('R','X') matches both plain
// remote-enabled modules ('R') and the basXML-capable ones SAP marks 'X' —
// SADT_REST_RFC_ENDPOINT among them — which a plain 'R' filter would hide.
func (s *Server) handleRFCSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pattern, _ := request.GetArguments()["pattern"].(string)

	like, err := funcnameLikePredicate(pattern)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}

	c, err := s.ensureRFCClient(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect via RFC: %v", err)), nil
	}

	where := "FUNCNAME LIKE '" + like + "' AND FMODE IN ( 'R', 'X' )"

	rows, err := saprfc.ReadTable(ctx, c, "TFDIR", where, []string{"FUNCNAME"}, 100)
	if err != nil {
		return newToolResultError(fmt.Sprintf("RFC search failed: %v", err)), nil
	}

	if len(rows) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No function modules found matching pattern: %s", pattern)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Function modules matching %q (%d):\n\n", pattern, len(rows))
	for _, row := range rows {
		fmt.Fprintf(&sb, "  %s\n", row["FUNCNAME"])
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// handleRFCGetMetadata describes a function module's interface as an
// MCP-tool-shaped JSON Schema (rfc.Client.DescribeTool), reading its DDIC
// signature directly over RFC instead of the ZADT_VSP WebSocket bridge.
// Structure and table parameters are expanded from their real DDIC layout —
// a strictly fuller signature than the old flat "[kind] name: type" listing.
func (s *Server) handleRFCGetMetadata(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	function, ok := request.GetArguments()["function"].(string)
	if !ok || function == "" {
		return newToolResultError("function is required"), nil
	}

	c, err := s.ensureRFCClient(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect via RFC: %v", err)), nil
	}

	function = strings.ToUpper(strings.TrimSpace(function))
	tool, err := c.DescribeTool(ctx, function)
	if err != nil {
		return newToolResultError(fmt.Sprintf("RFC getMetadata failed: %v", err)), nil
	}

	toolJSON, _ := json.MarshalIndent(tool, "", "  ")
	return mcp.NewToolResultText(fmt.Sprintf("Signature for %s:\n\n%s", function, string(toolJSON))), nil
}
