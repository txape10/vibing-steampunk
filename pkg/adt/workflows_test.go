package adt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// mockWorkflowTransport for testing workflows
type mockWorkflowTransport struct {
	responses map[string]*http.Response
	requests  []*http.Request
}

func (m *mockWorkflowTransport) Do(req *http.Request) (*http.Response, error) {
	m.requests = append(m.requests, req)

	path := req.URL.Path

	// "METHOD /path" exact match takes priority — lets a test disambiguate a
	// GET (e.g. an existence check) from a PUT to the same URL, which a
	// plain path key cannot express.
	if resp, ok := m.responses[req.Method+" "+path]; ok {
		return resp, nil
	}

	// Match by path
	if resp, ok := m.responses[path]; ok {
		return resp, nil
	}

	// Check for partial matches
	for key, resp := range m.responses {
		if strings.Contains(path, key) {
			return resp, nil
		}
	}

	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader("Not found")),
		Header:     http.Header{},
	}, nil
}

func newWorkflowTestResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"X-CSRF-Token": []string{"test-token"}},
	}
}

// TestClient_GetSource_Program tests GetSource for PROG type
func TestClient_GetSource_Program(t *testing.T) {
	sourceCode := `REPORT ztest.
WRITE: 'Hello, World!'.`

	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/programs/programs/ZTEST/source/main": newWorkflowTestResponse(sourceCode),
			"discovery": newWorkflowTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	result, err := client.GetSource(context.Background(), "PROG", "ZTEST", &GetSourceOptions{})
	if err != nil {
		t.Fatalf("GetSource failed: %v", err)
	}

	if result != sourceCode {
		t.Errorf("GetSource returned %q, want %q", result, sourceCode)
	}
}

// TestClient_GetSource_Class tests GetSource for CLAS type
func TestClient_GetSource_Class(t *testing.T) {
	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/oo/classes/ZCL_TEST": newWorkflowTestResponse(`{
				"definitions": "CLASS zcl_test DEFINITION PUBLIC.",
				"implementations": "CLASS zcl_test IMPLEMENTATION.\nENDCLASS.",
				"testclasses": "CLASS ltc_test DEFINITION FOR TESTING."
			}`),
			"discovery": newWorkflowTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	// Test without include parameter (returns full class)
	result, err := client.GetSource(context.Background(), "CLAS", "ZCL_TEST", &GetSourceOptions{})
	if err != nil {
		t.Fatalf("GetSource failed: %v", err)
	}

	if !strings.Contains(result, "definitions") {
		t.Errorf("GetSource didn't return class source")
	}
}

// TestClient_GetSource_Function tests GetSource for FUNC type
func TestClient_GetSource_Function(t *testing.T) {
	funcSource := `FUNCTION Z_TEST_FUNCTION.
ENDFUNCTION.`

	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/functions/groups/ZFUGR/fmodules/Z_TEST_FUNCTION/source/main": newWorkflowTestResponse(funcSource),
			"discovery": newWorkflowTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	result, err := client.GetSource(context.Background(), "FUNC", "Z_TEST_FUNCTION", &GetSourceOptions{
		Parent: "ZFUGR",
	})
	if err != nil {
		t.Fatalf("GetSource failed: %v", err)
	}

	if result != funcSource {
		t.Errorf("GetSource returned %q, want %q", result, funcSource)
	}
}

// TestClient_GetSource_InvalidType tests GetSource with invalid type
func TestClient_GetSource_InvalidType(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"discovery": newWorkflowTestResponse("OK"),
		},
	})
	client := NewClientWithTransport(cfg, transport)

	_, err := client.GetSource(context.Background(), "INVALID", "ZTEST", &GetSourceOptions{})
	if err == nil {
		t.Fatal("GetSource should fail with invalid type")
	}

	if !strings.Contains(err.Error(), "unsupported object type") {
		t.Errorf("Expected 'unsupported object type' error, got: %v", err)
	}
}

// TestClient_WriteSource_Create tests WriteSource in create mode
func TestClient_WriteSource_Create(t *testing.T) {
	sourceCode := `REPORT ztest.
WRITE: 'Hello, World!'.`

	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/programs/programs/ZTEST": newWorkflowTestResponse(`<?xml version="1.0"?>
<program:abapProgram xmlns:program="http://www.sap.com/adt/programs"
                     adtcore:name="ZTEST"
                     adtcore:type="PROG/P"
                     adtcore:responsible="USER"/>`),
			// The pre-create existence check (GetProgram) GETs this exact URL;
			// it must 404 so objectExists resolves to false and Create is not
			// rejected as "already exists". The later source PUT falls through
			// to the plain-path entry below (200 OK).
			"GET /sap/bc/adt/programs/programs/ZTEST/source/main": {
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("Not found")),
				Header:     http.Header{},
			},
			"/sap/bc/adt/programs/programs/ZTEST/source/main": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/checkruns":                           newWorkflowTestResponse("OK"),
			// parseActivationResult treats an empty body as a successful activation;
			// a non-empty non-XML body like "OK" is parsed as an activation error.
			"/sap/bc/adt/activation": newWorkflowTestResponse(""),
			// CreateAndActivateProgram checks the target package exists first
			// (avoids orphaning an ENQUEUE lock on a bad package name). The body
			// doesn't need to be valid nodestructure XML — packageExists treats
			// any non-404/"not found" GetPackage error as optimistically existing.
			"nodestructure": newWorkflowTestResponse("OK"),
			// Shell-create POST to the collection endpoint (distinct from the
			// specific object URL used for lock/source/activation above).
			"POST /sap/bc/adt/programs/programs": newWorkflowTestResponse("OK"),
			// newWorkflowTestResponse's CSRF header uses a non-canonical map key
			// ("X-CSRF-Token" vs the "X-Csrf-Token" http.Header.Get looks up), so
			// a real CSRF-token fetch — now exercised by the full create flow —
			// needs newDiscoveryOKResponse() instead.
			"discovery": newDiscoveryOKResponse(),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	result, err := client.WriteSource(context.Background(), "PROG", "ZTEST", sourceCode, &WriteSourceOptions{
		Mode:        WriteModeCreate,
		Description: "Test program",
		Package:     "$TMP",
	})
	if err != nil {
		t.Fatalf("WriteSource failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected WriteSource create to succeed, got message: %q", result.Message)
	}

	if result.ObjectURL == "" {
		t.Error("WriteSource should return object URL")
	}
}

// TestClient_WriteSource_Update_ExplicitMode_ObjectNotExists covers the bug where
// objectExists was only ever computed for Mode=Upsert — an explicit Mode=Update on
// a nonexistent object must still be rejected with "does not exist", not silently
// let through.
func TestClient_WriteSource_Update_ExplicitMode_ObjectNotExists(t *testing.T) {
	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"GET /sap/bc/adt/programs/programs/ZTEST/source/main": {
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("Not found")),
				Header:     http.Header{},
			},
			"discovery": newDiscoveryOKResponse(),
		},
	}
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	result, err := client.WriteSource(context.Background(), "PROG", "ZTEST", "REPORT ztest.", &WriteSourceOptions{
		Mode: WriteModeUpdate,
	})
	if err != nil {
		t.Fatalf("WriteSource returned a Go error instead of a rejected result: %v", err)
	}
	if result.Success {
		t.Fatal("expected explicit Update of a nonexistent object to fail")
	}
	if !strings.Contains(result.Message, "does not exist") {
		t.Errorf("expected message to mention the object does not exist, got: %q", result.Message)
	}
}

// TestClient_WriteSource_Create_ExplicitMode_ObjectAlreadyExists covers the other
// half of the same bug: explicit Mode=Create on an object that already exists must
// be rejected with "already exists" instead of silently proceeding to create it
// (previously objectExists was never computed for explicit Create either, so this
// case had zero coverage and zero enforcement).
func TestClient_WriteSource_Create_ExplicitMode_ObjectAlreadyExists(t *testing.T) {
	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"GET /sap/bc/adt/programs/programs/ZTEST/source/main": newWorkflowTestResponse("REPORT ztest. \" existing source"),
			"discovery": newDiscoveryOKResponse(),
		},
	}
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	result, err := client.WriteSource(context.Background(), "PROG", "ZTEST", "REPORT ztest.", &WriteSourceOptions{
		Mode:        WriteModeCreate,
		Description: "Test program",
		Package:     "$TMP",
	})
	if err != nil {
		t.Fatalf("WriteSource returned a Go error instead of a rejected result: %v", err)
	}
	if result.Success {
		t.Fatal("expected explicit Create of an already-existing object to fail")
	}
	if !strings.Contains(result.Message, "already exists") {
		t.Errorf("expected message to mention the object already exists, got: %q", result.Message)
	}
}

// TestClient_WriteSource_Update tests WriteSource in update mode
func TestClient_WriteSource_Update(t *testing.T) {
	sourceCode := `REPORT ztest.
WRITE: 'Updated!'.`

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/programs/programs/ZTEST": newWorkflowTestResponse(`<?xml version="1.0"?>
<program:abapProgram xmlns:program="http://www.sap.com/adt/programs"/>`),
			"discovery": newWorkflowTestResponse("OK"),
		},
	})
	client := NewClientWithTransport(cfg, transport)

	result, err := client.WriteSource(context.Background(), "PROG", "ZTEST", sourceCode, &WriteSourceOptions{
		Mode: WriteModeUpdate,
	})

	// WriteSource is a complex workflow - we just verify it doesn't error
	// Full workflow testing requires integration tests
	if err != nil {
		t.Fatalf("WriteSource failed: %v", err)
	}

	if result == nil {
		t.Fatal("WriteSource should return non-nil result")
	}

	// Verify it's in update mode (even if workflow didn't complete due to mocks)
	if result.ObjectType != "PROG" {
		t.Errorf("Expected ObjectType 'PROG', got %q", result.ObjectType)
	}
}

// TestClient_GrepObjects tests GrepObjects with multiple objects
func TestClient_GrepObjects(t *testing.T) {
	sourceCode1 := `REPORT ztest1.
DATA: lv_todo TYPE string. " TODO: implement this`

	sourceCode2 := `REPORT ztest2.
* TODO: fix this bug
WRITE: 'Hello'.`

	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/programs/programs/ZTEST1/source/main": newWorkflowTestResponse(sourceCode1),
			"/sap/bc/adt/programs/programs/ZTEST2/source/main": newWorkflowTestResponse(sourceCode2),
			"discovery": newWorkflowTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	result, err := client.GrepObjects(context.Background(), []string{
		"/sap/bc/adt/programs/programs/ZTEST1",
		"/sap/bc/adt/programs/programs/ZTEST2",
	}, "TODO", false, 0)

	if err != nil {
		t.Fatalf("GrepObjects failed: %v", err)
	}

	if len(result.Objects) != 2 {
		t.Errorf("Expected 2 objects with matches, got %d", len(result.Objects))
	}

	if result.TotalMatches != 2 {
		t.Errorf("Expected 2 total matches, got %d", result.TotalMatches)
	}
}

// TestClient_GrepObjects_NoMatches tests GrepObjects when pattern doesn't match
func TestClient_GrepObjects_NoMatches(t *testing.T) {
	sourceCode := `REPORT ztest.
WRITE: 'Hello, World!'.`

	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/programs/programs/ZTEST/source/main": newWorkflowTestResponse(sourceCode),
			"discovery": newWorkflowTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	result, err := client.GrepObjects(context.Background(), []string{
		"/sap/bc/adt/programs/programs/ZTEST",
	}, "NOTFOUND", false, 0)

	if err != nil {
		t.Fatalf("GrepObjects failed: %v", err)
	}

	if len(result.Objects) != 0 {
		t.Errorf("Expected 0 objects with matches, got %d", len(result.Objects))
	}

	if result.TotalMatches != 0 {
		t.Errorf("Expected 0 total matches, got %d", result.TotalMatches)
	}
}

// TestClient_GrepPackages tests GrepPackages with single package
func TestClient_GrepPackages(t *testing.T) {
	packageContents := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.sap.com/adt/packages" name="$TMP">
  <objects>
    <object type="PROG/P" name="ZTEST1" uri="/sap/bc/adt/programs/programs/ZTEST1"/>
  </objects>
</package>`

	sourceCode := `REPORT ztest1.
" TODO: complete implementation`

	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/packages/$TMP":                        newWorkflowTestResponse(packageContents),
			"/sap/bc/adt/programs/programs/ZTEST1/source/main": newWorkflowTestResponse(sourceCode),
			"discovery": newWorkflowTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	result, err := client.GrepPackages(context.Background(), []string{"$TMP"}, false, "TODO", false, []string{}, 0)

	if err != nil {
		t.Fatalf("GrepPackages failed: %v", err)
	}

	// Just verify it doesn't error - actual matching depends on GetPackage XML parsing
	if result == nil {
		t.Fatal("GrepPackages should return non-nil result")
	}

	if len(result.Packages) != 1 {
		t.Errorf("Expected 1 package in result, got %d", len(result.Packages))
	}
}

// TestClient_GrepPackages_Recursive tests GrepPackages with subpackage recursion
func TestClient_GrepPackages_Recursive(t *testing.T) {
	mainPackageContents := `<?xml version="1.0" encoding="UTF-8"?>
<package:package xmlns:package="http://www.sap.com/adt/packages">
  <package:subPackages>
    <package:package package:name="ZSUB1"/>
  </package:subPackages>
</package:package>`

	subPackageContents := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.sap.com/adt/packages" name="ZSUB1">
  <objects>
    <object type="PROG/P" name="ZTEST_SUB" uri="/sap/bc/adt/programs/programs/ZTEST_SUB"/>
  </objects>
</package>`

	sourceCode := `REPORT ztest_sub.
" TODO: implement`

	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/packages/ZMAIN":                          newWorkflowTestResponse(mainPackageContents),
			"/sap/bc/adt/packages/ZSUB1":                          newWorkflowTestResponse(subPackageContents),
			"/sap/bc/adt/programs/programs/ZTEST_SUB/source/main": newWorkflowTestResponse(sourceCode),
			"discovery": newWorkflowTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	// Test with recursive flag
	result, err := client.GrepPackages(context.Background(), []string{"ZMAIN"}, true, "TODO", false, []string{}, 0)

	if err != nil {
		t.Fatalf("GrepPackages failed: %v", err)
	}

	if len(result.Packages) == 0 {
		t.Error("Expected results from recursive search")
	}
}

// TestClient_GrepPackages_MultiplePackages tests GrepPackages with array of packages
func TestClient_GrepPackages_MultiplePackages(t *testing.T) {
	packageContents := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.sap.com/adt/packages" name="$TMP">
  <objects>
    <object type="PROG/P" name="ZTEST1" uri="/sap/bc/adt/programs/programs/ZTEST1"/>
  </objects>
</package>`

	sourceCode := `REPORT ztest1.
" FIXME: bug here`

	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"/sap/bc/adt/packages/$TMP":                        newWorkflowTestResponse(packageContents),
			"/sap/bc/adt/packages/$LOCAL":                      newWorkflowTestResponse(packageContents),
			"/sap/bc/adt/programs/programs/ZTEST1/source/main": newWorkflowTestResponse(sourceCode),
			"discovery": newWorkflowTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	// Test with multiple packages
	result, err := client.GrepPackages(context.Background(), []string{"$TMP", "$LOCAL"}, false, "FIXME", false, []string{}, 0)

	if err != nil {
		t.Fatalf("GrepPackages failed: %v", err)
	}

	if len(result.Packages) == 0 {
		t.Error("Expected results from multiple packages")
	}
}

// TestExecuteABAPResult tests the ExecuteABAPResult struct
func TestExecuteABAPResult(t *testing.T) {
	result := &ExecuteABAPResult{
		Success:       true,
		ProgramName:   "ZTEMP_EXEC_12345678",
		Output:        []string{"Hello from SAP"},
		ExecutionTime: 1234,
		CleanedUp:     true,
		Message:       "Executed successfully, 1 output(s) returned",
	}

	if !result.Success {
		t.Error("Expected Success to be true")
	}

	if result.ProgramName != "ZTEMP_EXEC_12345678" {
		t.Errorf("Expected ProgramName ZTEMP_EXEC_12345678, got %s", result.ProgramName)
	}

	if len(result.Output) != 1 {
		t.Errorf("Expected 1 output, got %d", len(result.Output))
	}

	if result.Output[0] != "Hello from SAP" {
		t.Errorf("Expected output 'Hello from SAP', got %s", result.Output[0])
	}

	if result.ExecutionTime != 1234.0 {
		t.Errorf("Expected ExecutionTime 1234.0, got %f", result.ExecutionTime)
	}

	if !result.CleanedUp {
		t.Error("Expected CleanedUp to be true")
	}
}

// TestExecuteABAPOptions tests the ExecuteABAPOptions struct defaults
func TestExecuteABAPOptions(t *testing.T) {
	// Test with nil options - should use defaults
	opts := &ExecuteABAPOptions{}

	if opts.RiskLevel != "" {
		t.Error("Expected empty RiskLevel for new options")
	}

	if opts.ReturnVariable != "" {
		t.Error("Expected empty ReturnVariable for new options")
	}

	if opts.ProgramPrefix != "" {
		t.Error("Expected empty ProgramPrefix for new options")
	}

	// Test with values
	opts = &ExecuteABAPOptions{
		RiskLevel:      "dangerous",
		ReturnVariable: "lv_custom",
		KeepProgram:    true,
		ProgramPrefix:  "ZEXEC_",
	}

	if opts.RiskLevel != "dangerous" {
		t.Errorf("Expected RiskLevel 'dangerous', got %s", opts.RiskLevel)
	}

	if opts.ReturnVariable != "lv_custom" {
		t.Errorf("Expected ReturnVariable 'lv_custom', got %s", opts.ReturnVariable)
	}

	if !opts.KeepProgram {
		t.Error("Expected KeepProgram to be true")
	}

	if opts.ProgramPrefix != "ZEXEC_" {
		t.Errorf("Expected ProgramPrefix 'ZEXEC_', got %s", opts.ProgramPrefix)
	}
}

// Test line ending normalization for EditSource
func TestNormalizeLineEndings(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"CRLF to LF", "line1\r\nline2\r\nline3", "line1\nline2\nline3"},
		{"Already LF", "line1\nline2\nline3", "line1\nline2\nline3"},
		{"Mixed", "line1\r\nline2\nline3\r\n", "line1\nline2\nline3\n"},
		{"No newlines", "single line", "single line"},
		{"Empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeLineEndings(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeLineEndings(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// Test countMatches with CRLF vs LF
func TestCountMatches_LineEndings(t *testing.T) {
	// Source with CRLF (like SAP returns)
	source := "IF a = b.\r\n  WRITE: / 'hello'.\r\nENDIF."

	// Search pattern with LF (like AI sends)
	pattern := "IF a = b.\n  WRITE: / 'hello'.\nENDIF."

	// Should match despite different line endings
	count := countMatches(source, pattern, false)
	if count != 1 {
		t.Errorf("countMatches with CRLF source and LF pattern = %d, want 1", count)
	}

	// Case insensitive should also work
	count = countMatches(source, strings.ToLower(pattern), true)
	if count != 1 {
		t.Errorf("countMatches case-insensitive = %d, want 1", count)
	}
}

// Test replaceMatches with CRLF vs LF
func TestReplaceMatches_LineEndings(t *testing.T) {
	// Source with CRLF
	source := "line1.\r\nOLD CODE.\r\nline3."

	// Old/new patterns with LF
	old := "OLD CODE."
	newStr := "NEW CODE.\nEXTRA LINE."

	// Replace should work and result should have LF normalized
	result := replaceMatches(source, old, newStr, false, false)
	expected := "line1.\nNEW CODE.\nEXTRA LINE.\nline3."

	if result != expected {
		t.Errorf("replaceMatches result = %q, want %q", result, expected)
	}
}

// --- Issue #144: WriteProgram/WriteClass must adopt corrNr from lock result ---

// newDiscoveryOKResponse returns a discovery response carrying a CSRF token
// under its canonical header key. newWorkflowTestResponse's literal
// "X-CSRF-Token" map key is not the canonical form http.Header.Get looks
// up ("X-Csrf-Token"), so it never actually satisfies the CSRF fetch —
// tests relying only on it silently fall through to internal failures
// instead of exercising the real write path.
func newDiscoveryOKResponse() *http.Response {
	h := http.Header{}
	h.Set("X-Csrf-Token", "test-token")
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("OK")),
		Header:     h,
	}
}

// cleanSyntaxCheckXML is a minimal "no errors" ADT syntax-check response.
const cleanSyntaxCheckXML = `<?xml version="1.0" encoding="UTF-8"?>
<chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <chkrun:checkReport chkrun:status="clean">
    <chkrun:checkMessageList/>
  </chkrun:checkReport>
</chkrun:checkRunReports>`

// findRequestByPath returns the last recorded request whose path contains substr.
func findRequestByPath(requests []*http.Request, substr string) *http.Request {
	var found *http.Request
	for _, req := range requests {
		if strings.Contains(req.URL.Path, substr) {
			found = req
		}
	}
	return found
}

func TestClient_WriteProgram_AdoptsCorrNrFromLock_WhenTransportEmpty(t *testing.T) {
	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"checkruns":  newWorkflowTestResponse(cleanSyntaxCheckXML),
			"activation": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/programs/programs/ZTEST/source/main": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/programs/programs/ZTEST":             newWorkflowTestResponse(lockResponseXML),
			"discovery":                                       newDiscoveryOKResponse(),
		},
	}
	// Adoption re-validates against the transportable-edit policy (issue #144
	// follow-up), so it must be explicitly allowed for the adopted corrNr to
	// reach UpdateSource.
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowTransportableEdits())
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	if _, err := client.WriteProgram(context.Background(), "ZTEST", "REPORT ztest.", ""); err != nil {
		t.Fatalf("WriteProgram failed: %v", err)
	}

	putReq := findRequestByPath(mock.requests, "/source/main")
	if putReq == nil {
		t.Fatal("expected a request to the source URL")
	}
	if got := putReq.URL.Query().Get("corrNr"); got != "D15K000001" {
		t.Errorf("expected UpdateSource to adopt corrNr from lock result, got corrNr=%q", got)
	}
}

func TestClient_WriteProgram_BlocksCorrNrAdoption_WhenTransportableEditsDisabled(t *testing.T) {
	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"checkruns":  newWorkflowTestResponse(cleanSyntaxCheckXML),
			"activation": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/programs/programs/ZTEST/source/main": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/programs/programs/ZTEST":             newWorkflowTestResponse(lockResponseXML),
			"discovery":                                       newDiscoveryOKResponse(),
		},
	}
	// AllowTransportableEdits defaults to false. Even though the lock response
	// carries a real corrNr (the object is already in an open request), WriteProgram
	// must not silently adopt it and write — that would bypass the safety gate.
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	result, err := client.WriteProgram(context.Background(), "ZTEST", "REPORT ztest.", "")
	if err != nil {
		t.Fatalf("WriteProgram returned a Go error instead of a blocked result: %v", err)
	}
	if result.Success {
		t.Fatal("expected WriteProgram to fail when adopting a transport with transportable edits disabled")
	}
	if !strings.Contains(result.Message, "Transportable-edit check failed") {
		t.Errorf("expected message to mention the blocked policy check, got: %q", result.Message)
	}
	if putReq := findRequestByPath(mock.requests, "/source/main"); putReq != nil {
		t.Error("expected no write request to reach UpdateSource when transport adoption is blocked")
	}
}

func TestClient_WriteProgram_KeepsExplicitTransport(t *testing.T) {
	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"checkruns":  newWorkflowTestResponse(cleanSyntaxCheckXML),
			"activation": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/programs/programs/ZTEST/source/main": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/programs/programs/ZTEST":             newWorkflowTestResponse(lockResponseXML),
			"discovery":                                       newDiscoveryOKResponse(),
		},
	}
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowTransportableEdits())
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	if _, err := client.WriteProgram(context.Background(), "ZTEST", "REPORT ztest.", "D99K123456"); err != nil {
		t.Fatalf("WriteProgram failed: %v", err)
	}

	putReq := findRequestByPath(mock.requests, "/source/main")
	if putReq == nil {
		t.Fatal("expected a request to the source URL")
	}
	if got := putReq.URL.Query().Get("corrNr"); got != "D99K123456" {
		t.Errorf("expected caller-supplied transport to take precedence, got corrNr=%q", got)
	}
}

func TestClient_WriteClass_AdoptsCorrNrFromLock_WhenTransportEmpty(t *testing.T) {
	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"checkruns":  newWorkflowTestResponse(cleanSyntaxCheckXML),
			"activation": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/oo/classes/ZCL_TEST/source/main": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/oo/classes/ZCL_TEST":             newWorkflowTestResponse(lockResponseXML),
			"discovery":                                   newDiscoveryOKResponse(),
		},
	}
	// Adoption re-validates against the transportable-edit policy (issue #144
	// follow-up), so it must be explicitly allowed for the adopted corrNr to
	// reach UpdateSource.
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowTransportableEdits())
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	if _, err := client.WriteClass(context.Background(), "ZCL_TEST", "CLASS zcl_test DEFINITION.", ""); err != nil {
		t.Fatalf("WriteClass failed: %v", err)
	}

	putReq := findRequestByPath(mock.requests, "/source/main")
	if putReq == nil {
		t.Fatal("expected a request to the source URL")
	}
	if got := putReq.URL.Query().Get("corrNr"); got != "D15K000001" {
		t.Errorf("expected UpdateSource to adopt corrNr from lock result, got corrNr=%q", got)
	}
}

// objectStructureXMLWithMethod builds a minimal class objectstructure response
// with a single method whose implementation spans the given line range.
func objectStructureXMLWithMethod(className, methodName string, implStart, implEnd int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<abapsource:objectStructureElement xmlns:abapsource="http://www.sap.com/adt/abapsource" name="%s" type="CLAS/OC">
  <abapsource:objectStructureElement name="%s" type="CLAS/OM">
    <atom:link xmlns:atom="http://www.w3.org/2005/Atom"
      href="./../%s/source/main#start=%d,2;end=%d,11"
      rel="http://www.sap.com/adt/relations/source/implementationBlock"/>
  </abapsource:objectStructureElement>
</abapsource:objectStructureElement>`, className, methodName, strings.ToLower(className), implStart, implEnd)
}

// TestClient_WriteClassMethod_BlocksCorrNrAdoption_WhenTransportableEditsDisabled covers
// the method-level update path (WriteSource with opts.Method), which routes through
// writeClassMethodUpdate — a 6th call site for the same corrNr-adoption pattern as
// WriteProgram/WriteClass/WriteInclude/EditSourceWithOptions/DeleteObjectWithAutoLock.
func TestClient_WriteClassMethod_BlocksCorrNrAdoption_WhenTransportableEditsDisabled(t *testing.T) {
	const classSource = "CLASS zcl_test IMPLEMENTATION.\nMETHOD get_data.\n  rv_result = 1.\nENDMETHOD.\nENDCLASS.\n"

	mock := &mockWorkflowTransport{
		responses: map[string]*http.Response{
			"checkruns":  newWorkflowTestResponse(cleanSyntaxCheckXML),
			"activation": newWorkflowTestResponse("OK"),
			"/sap/bc/adt/oo/classes/ZCL_TEST/objectstructure": newWorkflowTestResponse(objectStructureXMLWithMethod("ZCL_TEST", "GET_DATA", 2, 4)),
			"/sap/bc/adt/oo/classes/ZCL_TEST/source/main":     newWorkflowTestResponse(classSource),
			// writeClassMethodUpdate locks the lowercased object URL (unlike WriteClass).
			"/sap/bc/adt/oo/classes/zcl_test": newWorkflowTestResponse(lockResponseXML),
			"discovery":                       newDiscoveryOKResponse(),
		},
	}
	// AllowTransportableEdits defaults to false. Even though the lock response
	// carries a real corrNr, writeClassMethodUpdate must not silently adopt it.
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	// Calls writeClassMethodUpdate directly — going through the public WriteSource
	// dispatcher would additionally exercise its pre-existing (unrelated) Upsert/
	// objectExists resolution quirks, which are out of scope here.
	result, err := client.writeClassMethodUpdate(context.Background(), "ZCL_TEST", "GET_DATA", "METHOD get_data.\n  rv_result = 2.\nENDMETHOD.", "")
	if err != nil {
		t.Fatalf("writeClassMethodUpdate returned a Go error instead of a blocked result: %v", err)
	}
	if result.Success {
		t.Fatal("expected method-level update to fail when adopting a transport with transportable edits disabled")
	}
	if !strings.Contains(result.Message, "Transportable-edit check failed") {
		t.Errorf("expected message to mention the blocked policy check, got: %q", result.Message)
	}
	putReq := findRequestByPath(mock.requests, "/sap/bc/adt/oo/classes/ZCL_TEST/source/main")
	if putReq != nil && putReq.Method == http.MethodPut {
		t.Error("expected no PUT to reach UpdateSource when transport adoption is blocked")
	}
}
