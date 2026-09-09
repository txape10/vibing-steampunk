package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestNewToolResultError(t *testing.T) {
	result := newToolResultError("test error message")

	if result == nil {
		t.Fatal("newToolResultError returned nil")
	}

	if !result.IsError {
		t.Error("IsError should be true")
	}

	if len(result.Content) != 1 {
		t.Fatalf("Expected 1 content item, got %d", len(result.Content))
	}

	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("Content should be TextContent, got %T", result.Content[0])
	}

	if textContent.Text != "test error message" {
		t.Errorf("Text = %v, want 'test error message'", textContent.Text)
	}
}

func TestConfig(t *testing.T) {
	cfg := &Config{
		BaseURL:            "https://sap.example.com:44300",
		Username:           "testuser",
		Password:           "testpass",
		Client:             "100",
		Language:           "DE",
		InsecureSkipVerify: true,
	}

	if cfg.BaseURL != "https://sap.example.com:44300" {
		t.Errorf("BaseURL = %v, want https://sap.example.com:44300", cfg.BaseURL)
	}
	if cfg.Username != "testuser" {
		t.Errorf("Username = %v, want testuser", cfg.Username)
	}
	if cfg.Password != "testpass" {
		t.Errorf("Password = %v, want testpass", cfg.Password)
	}
	if cfg.Client != "100" {
		t.Errorf("Client = %v, want 100", cfg.Client)
	}
	if cfg.Language != "DE" {
		t.Errorf("Language = %v, want DE", cfg.Language)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true")
	}
}

func TestNewServer(t *testing.T) {
	cfg := &Config{
		BaseURL:  "https://sap.example.com:44300",
		Username: "testuser",
		Password: "testpass",
		Client:   "001",
		Language: "EN",
	}

	server := NewServer(cfg)

	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	if server.mcpServer == nil {
		t.Error("MCP server should not be nil")
	}
	if server.adtClient == nil {
		t.Error("ADT client should not be nil")
	}
}

func TestDebuggerGetVariablesSchemaIncludesItems(t *testing.T) {
	cfg := &Config{
		BaseURL:  "https://sap.example.com:44300",
		Username: "testuser",
		Password: "testpass",
		Client:   "001",
		Language: "EN",
	}

	server := NewServer(cfg)
	if server == nil || server.mcpServer == nil {
		t.Fatal("server or MCP server is nil")
	}

	rawResponse := server.mcpServer.HandleMessage(context.Background(), []byte(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/list",
		"params": {}
	}`))

	response, ok := rawResponse.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", rawResponse)
	}

	var tools []mcp.Tool
	switch result := response.Result.(type) {
	case mcp.ListToolsResult:
		tools = result.Tools
	case *mcp.ListToolsResult:
		tools = result.Tools
	default:
		t.Fatalf("expected ListToolsResult, got %T", response.Result)
	}

	var debuggerTool *mcp.Tool
	for i := range tools {
		if tools[i].Name == "DebuggerGetVariables" {
			debuggerTool = &tools[i]
			break
		}
	}
	if debuggerTool == nil {
		t.Fatal("DebuggerGetVariables tool not found")
	}

	variableIDsRaw, ok := debuggerTool.InputSchema.Properties["variable_ids"]
	if !ok {
		t.Fatal("variable_ids property not found in DebuggerGetVariables schema")
	}

	variableIDs, ok := variableIDsRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected variable_ids schema to be map[string]interface{}, got %T", variableIDsRaw)
	}

	if variableIDs["type"] != "array" {
		t.Fatalf("expected variable_ids type to be 'array', got %v", variableIDs["type"])
	}

	itemsRaw, ok := variableIDs["items"]
	if !ok {
		t.Fatal("variable_ids array schema is missing items")
	}

	items, ok := itemsRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected items to be map[string]interface{}, got %T", itemsRaw)
	}

	if items["type"] != "string" {
		t.Fatalf("expected variable_ids.items.type to be 'string', got %v", items["type"])
	}
}

// TestWriteMessageClassTextsSchemaIncludesTexts guards against the tool being
// uncallable again: before this fix the registered schema had no "texts"
// property at all, even though the handler required it — every call failed
// with "texts is required" regardless of what the caller sent, because the
// schema never offered the parameter in the first place.
func TestWriteMessageClassTextsSchemaIncludesTexts(t *testing.T) {
	cfg := &Config{
		BaseURL:  "https://sap.example.com:44300",
		Username: "testuser",
		Password: "testpass",
		Client:   "001",
		Language: "EN",
		Mode:     "expert", // WriteMessageClassTexts is not in the focused-mode whitelist
	}

	server := NewServer(cfg)
	if server == nil || server.mcpServer == nil {
		t.Fatal("server or MCP server is nil")
	}

	rawResponse := server.mcpServer.HandleMessage(context.Background(), []byte(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/list",
		"params": {}
	}`))

	response, ok := rawResponse.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", rawResponse)
	}

	var tools []mcp.Tool
	switch result := response.Result.(type) {
	case mcp.ListToolsResult:
		tools = result.Tools
	case *mcp.ListToolsResult:
		tools = result.Tools
	default:
		t.Fatalf("expected ListToolsResult, got %T", response.Result)
	}

	var tool *mcp.Tool
	for i := range tools {
		if tools[i].Name == "WriteMessageClassTexts" {
			tool = &tools[i]
			break
		}
	}
	if tool == nil {
		t.Fatal("WriteMessageClassTexts tool not found")
	}

	for _, prop := range []string{"texts", "delete_numbers"} {
		raw, ok := tool.InputSchema.Properties[prop]
		if !ok {
			t.Fatalf("%s property not found in WriteMessageClassTexts schema — the tool would be uncallable for that argument", prop)
		}
		schema, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("expected %s schema to be map[string]interface{}, got %T", prop, raw)
		}
		if schema["type"] != "array" {
			t.Fatalf("expected %s type to be 'array', got %v", prop, schema["type"])
		}
	}
}
