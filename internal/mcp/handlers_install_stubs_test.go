package mcp

import "testing"

// InstallAbapGit and InstallDummyTest were stubs (one printed a plan and
// deployed nothing, the other only exercised the install workflow). They are
// gone from the MCP surface; ListDependencies stays.
func TestStubInstallToolsAreNotRegistered(t *testing.T) {
	cfg := &Config{BaseURL: "http://127.0.0.1:1", Username: "u", Password: "p", Client: "001", Language: "EN", Mode: "expert"}
	server := NewServer(cfg)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	tools := server.mcpServer.ListTools()
	for _, gone := range []string{"InstallAbapGit", "InstallDummyTest"} {
		if _, ok := tools[gone]; ok {
			t.Errorf("%s is still registered", gone)
		}
	}
	if _, ok := tools["ListDependencies"]; !ok {
		t.Error("ListDependencies must stay registered")
	}
}
