package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

const inventoryTestTable = `<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">` +
	`<dataPreview:columns><dataPreview:metadata dataPreview:name="OBJECT" dataPreview:type="C" dataPreview:length="4"/><dataPreview:dataSet><dataPreview:data>CLAS</dataPreview:data></dataPreview:dataSet></dataPreview:columns>` +
	`<dataPreview:columns><dataPreview:metadata dataPreview:name="OBJ_NAME" dataPreview:type="C" dataPreview:length="30"/><dataPreview:dataSet><dataPreview:data>ZCL_A</dataPreview:data></dataPreview:dataSet></dataPreview:columns>` +
	`</dataPreview:tableData>`

type inventoryServerStub struct {
	mu    sync.Mutex
	paths []string
}

func (s *inventoryServerStub) mutating() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, p := range s.paths {
		reads := strings.Contains(p, "/datapreview/") || strings.Contains(p, "/repository/nodestructure")
		if !strings.HasPrefix(p, "GET ") && !strings.HasPrefix(p, "HEAD ") && !reads {
			out = append(out, p)
		}
	}
	return out
}

func newInventoryServer(t *testing.T, mutate func(*Config)) (*Server, *inventoryServerStub) {
	t.Helper()
	stub := &inventoryServerStub{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.paths = append(stub.paths, r.Method+" "+r.URL.Path)
		stub.mu.Unlock()
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if strings.HasSuffix(r.URL.Path, "/datapreview/freestyle") {
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(strings.ToLower(string(raw)), "from tadir") {
				_, _ = io.WriteString(w, inventoryTestTable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	cfg := &Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN", Mode: "hyperfocused"}
	if mutate != nil {
		mutate(cfg)
	}
	return NewServer(cfg), stub
}

func packageRequest(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// SAP(action="read", target="DEVC $PKG", params={"inventory": true}) reaches
// the inventory; without the parameter the package read is unchanged.
func TestReadPackageInventoryRoute(t *testing.T) {
	s, stub := newInventoryServer(t, nil)

	res, err := s.handleUniversalTool(context.Background(), packageRequest(map[string]any{
		"action": "read", "target": "DEVC ZDEMO", "params": map[string]any{"inventory": true},
	}))
	if err != nil || res.IsError {
		t.Fatalf("inventory failed: %v %s", err, resultText(res))
	}
	var inv struct {
		Package string `json:"package"`
		Objects []struct {
			Name string `json:"name"`
		} `json:"objects"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &inv); err != nil {
		t.Fatalf("not JSON: %v: %s", err, resultText(res))
	}
	if inv.Package != "ZDEMO" || len(inv.Objects) != 1 || inv.Objects[0].Name != "ZCL_A" {
		t.Fatalf("got %s", resultText(res))
	}

	stub.mu.Lock()
	stub.paths = nil
	stub.mu.Unlock()
	if _, err := s.handleUniversalTool(context.Background(), packageRequest(map[string]any{
		"action": "read", "target": "DEVC ZDEMO",
	})); err != nil {
		t.Fatal(err)
	}
	for _, p := range stub.paths {
		if strings.Contains(p, "/datapreview/") {
			t.Fatalf("without inventory the package read must not query tables: %v", stub.paths)
		}
	}
}

// The inventory only reads: under --read-only it still answers, and nothing
// that changes SAP leaves the process.
func TestPackageInventoryUnderReadOnly(t *testing.T) {
	s, stub := newInventoryServer(t, func(c *Config) { c.ReadOnly = true })
	res, err := s.handleGetPackage(context.Background(), packageRequest(map[string]any{
		"package_name": "ZDEMO", "inventory": true,
	}))
	if err != nil || res.IsError {
		t.Fatalf("read-only inventory failed: %v %s", err, resultText(res))
	}
	if strings.Contains(resultText(res), "blocked by safety configuration") {
		t.Fatalf("inventory refused under --read-only: %s", resultText(res))
	}
	if m := stub.mutating(); len(m) != 0 {
		t.Fatalf("inventory sent a request that is not a read: %v", m)
	}
}

// An invalid package name is refused before any request.
func TestPackageInventoryRefusesBadName(t *testing.T) {
	s, stub := newInventoryServer(t, nil)
	res, _ := s.handleGetPackage(context.Background(), packageRequest(map[string]any{
		"package_name": "ZX' OR 1=1", "inventory": true,
	}))
	if !res.IsError || !strings.Contains(resultText(res), "invalid package name") {
		t.Fatalf("want a refusal, got %s", resultText(res))
	}
	if len(stub.paths) != 0 {
		t.Fatalf("a refused name must send nothing: %v", stub.paths)
	}
}
