package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// The schema half matters as much as the handler half: while the published
// schema says lock_handle is required, the client demands one and the agent
// calls LockObject first — the cross-call window of #169 is back.
func TestSelfLockingToolsDoNotDemandAHandle(t *testing.T) {
	cfg := &Config{BaseURL: "http://127.0.0.1:1", Username: "u", Password: "p", Client: "001", Language: "EN", Mode: "expert"}
	server := NewServer(cfg)
	tools := server.mcpServer.ListTools()
	for _, name := range []string{"UpdateSource", "CreateTestInclude", "UpdateClassInclude"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		for _, req := range tool.Tool.InputSchema.Required {
			if req == "lock_handle" {
				t.Errorf("%s still requires lock_handle", name)
			}
		}
	}
	unlock := tools["UnlockObject"]
	found := false
	for _, req := range unlock.Tool.InputSchema.Required {
		found = found || req == "lock_handle"
	}
	if !found {
		t.Error("UnlockObject must keep lock_handle required: without one it has nothing to release")
	}
}

// Focused mode must not offer a lock nothing can spend.
func TestFocusedModeHasNothingThatSpendsALockHandle(t *testing.T) {
	set := focusedToolSet()
	spenders := []string{"UpdateSource", "DeleteObject", "CreateTestInclude", "UpdateClassInclude"}
	any := false
	for _, n := range spenders {
		any = any || set[n]
	}
	if !any && (set["LockObject"] || set["UnlockObject"]) {
		t.Error("focused mode exposes LockObject/UnlockObject but no tool that accepts a handle: a lock taken there can only be stranded")
	}
}

// Without a handle UpdateSource locks, writes and unlocks inside one call.
func TestUpdateSourceWithoutHandleTakesItsOwnLock(t *testing.T) {
	server, rec := newDescTestServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"object_url": "/sap/bc/adt/programs/programs/zdemo_prog",
		"source":     "REPORT zdemo_prog.\n",
	}
	res, err := server.handleUpdateSource(context.Background(), req)
	if err != nil || res.IsError {
		t.Fatalf("UpdateSource without a handle failed: %v %+v", err, res)
	}
	for _, want := range []string{"_action=lock", "put ", "_action=unlock"} {
		if !rec.hit(want) {
			t.Errorf("expected a request containing %q; trace: %v", want, rec.calls)
		}
	}
}
