package mcp

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// The read-only invariant (upstream #288): under --read-only no registered
// tool may send a mutating request to SAP or open an RFC connection. Every
// tool must be classified here, so a new write tool that forgets its gate
// (or a new tool nobody classified) fails this test instead of shipping.

// readOnlyWriteTools are the tools that change SAP or run code on it.
var readOnlyWriteTools = []string{
	"Activate", "ActivateMultiple", "ActivatePackage", "CallRFC", "CloneObject",
	"CreateAndActivateProgram", "CreateClassWithTests", "CreateDataElement", "CreateDomain",
	"CreateLockObject", "CreateMessageClass", "CreateObject", "CreatePackage", "CreateStructure",
	"CreateTable", "CreateTableType", "CreateTestInclude", "CreateTransport", "DeleteObject",
	"DeleteTransport", "DeployFromFile", "DeployZip", "EditSource", "ExecuteABAP", "ImportFromFile",
	"InstallZADTVSP", "LockObject", "MoveObject", "PublishServiceBinding", "RecoverFailedCreate",
	"ReleaseTransport", "RenameObject", "RunReport", "RunReportAsync", "SetClassParamText",
	"SetDescription", "SetPrettyPrinterSettings", "SetTextElements", "TextsSet", "UI5CreateApp",
	"UI5DeleteApp", "UI5DeleteFile", "UI5UploadFile", "UnpublishServiceBinding", "UpdateClassInclude",
	"UpdateSource", "WriteClass", "WriteDataElementLabels", "WriteMessageClassTexts", "WriteProgram",
	"WriteSource",
}

// readOnlyAllowedTools are classified as not writing to SAP. Debugger
// breakpoints/AMDP and UnlockObject stay allowed on purpose (2ap): they are what
// debugging a read-only system and releasing a lock need.
var readOnlyAllowedTools = []string{
	"AMDPDebuggerResume", "AMDPDebuggerStart", "AMDPDebuggerStep", "AMDPDebuggerStop", "AMDPGetBreakpoints",
	"AMDPGetVariables", "AMDPSetBreakpoint", "AnalyzeABAPCode", "AnalyzeCallGraph", "CheckBoundaries",
	"CodeCompletion", "CompareCallGraphs", "CompareLanguages", "CompareSource", "CompareVersions",
	"DebuggerAttach", "DebuggerDetach", "DebuggerGetStack", "DebuggerGetVariables", "DebuggerListen",
	"DebuggerStep", "DeleteBreakpoint", "ExportToFile", "FindDefinition", "FindReferences",
	"GetAPIReleaseState", "GetATCCustomizing", "GetAbapHelp", "GetAsyncResult", "GetBreakpoints",
	"GetCDSDependencies", "GetCDSElementInfo", "GetCDSImpactAnalysis", "GetCallGraph", "GetCalleesOf",
	"GetCallersOf", "GetCheckRunResults", "GetClass", "GetClassComponents", "GetClassInclude",
	"GetClassInfo", "GetClassParamTexts", "GetCodeCoverage", "GetConnectionInfo", "GetContext",
	"GetDataElementLabels", "GetDescription", "GetDump", "GetFeatures", "GetFunction", "GetFunctionGroup",
	"GetInactiveObjects", "GetInclude", "GetInstalledComponents", "GetInterface", "GetMessageClassTexts",
	"GetMessages", "GetObjectStructure", "GetObjectTextsInLanguage", "GetPackage", "GetPrettyPrinterSettings",
	"GetProgram", "GetReportJobStatus", "GetRevisionSource", "GetRevisions", "GetSQLTraceState", "GetSource",
	"GetStructure", "GetSystemInfo", "GetTable", "GetTableContents", "GetTextElements", "GetTextPool",
	"GetTrace", "GetTransaction", "GetTransport", "GetTransportInfo", "GetTypeHierarchy", "GetTypeInfo",
	"GetUserTransports", "GetVariants", "GitExport", "GitTypes", "GraphStats", "GrepObject", "GrepObjects",
	"GrepPackage", "GrepPackages", "HardcodeAudit", "HardcodeUsage", "ListDependencies", "ListDumps",
	"ListSQLTraces", "ListTraces", "ListTransports", "PrettyPrint", "RunATCCheck", "RunQuery",
	"RunUnitTests", "SaveToFile", "SearchObject", "SetBreakpoint", "SyntaxCheck", "TextsGet",
	"TraceExecution", "UI5GetApp", "UI5GetFileContent", "UI5ListApps", "UnlockObject",
}

// invariantOverrides are the arguments a tool needs that clash with the shared set.
var invariantOverrides = map[string]map[string]any{
	"CreateObject": {"object_type": "PROG/P"},
	"TextsSet":     {"texts": map[string]any{"P_DEVC": "demo"}},
}

// invariantArgs is a superset of the argument names the handlers look for, so
// a call gets past parameter validation and reaches (or fails to reach) its gate.
func invariantArgs() map[string]any {
	return map[string]any{
		"name": "ZDEMO_INV", "object_name": "ZDEMO_INV", "object_type": "PROG", "object_url": "/sap/bc/adt/programs/programs/zdemo_inv",
		"source": "abapgit-standalone", "package": "$TMP", "package_name": "$TMP", "description": "demo",
		"transport": "A4HK000001", "lock_handle": "HANDLE", "function": "RFC_PING", "report": "ZDEMO",
		"program": "ZDEMO_INV", "class_name": "ZCL_DEMO_INV", "new_name": "ZDEMO_NEW", "new_package": "ZDEMO_PKG",
		"old_string": "a", "new_string": "b", "objType": "PROG/P", "file_path": "x.abap", "objects": []any{"PROG ZDEMO_INV"},
		"fields": `[{"name":"F1","type":"CHAR","length":1,"key":true}]`, "text": "demo", "parameter": "IV_A", "method": "M", "app_name": "ZDEMO", "path": "x",
		"binding_name": "ZDEMO", "version": "0001", "language": "EN", "type": "PROG", "title": "demo",
		"include_type": "testclasses", "message_class": "ZDEMO", "messages": "[]", "short": "a", "short_text": "a",
		"selection_texts": "{}", "text_symbols": "{}", "settings": "{}", "dry_run": false,
		"program_name": "ZDEMO_INV", "class_source": "CLASS zcl_demo_inv DEFINITION. ENDCLASS.", "type_name": "CHAR10",
		"data_type": "CHAR", "primary_table": "T000", "row_type_name": "BAPIRET2", "code": "WRITE 1.",
		"service_name": "ZDEMO_SRV", "oldName": "ZDEMO_INV", "newName": "ZDEMO_NEW", "indentation": true, "style": "keywordUpper", "test_source": "x", "packageName": "$TMP",
		"texts": []any{map[string]any{"number": "001", "text": "a"}}, "content": "x", "source_name": "ZDEMO_INV", "target_name": "ZDEMO_NEW",
	}
}

func invariantCall(t *testing.T, s *Server, name string, override map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool, ok := s.mcpServer.ListTools()[name]
	if !ok {
		t.Fatalf("tool %s is not registered", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	args := invariantArgs()
	// handlers that read the file before they gate need one to read
	file := filepath.Join(t.TempDir(), "zdemo_inv.prog.abap")
	if err := os.WriteFile(file, []byte("REPORT zdemo_inv."), 0o600); err != nil {
		t.Fatal(err)
	}
	args["file_path"] = file
	for k, v := range invariantOverrides[name] {
		args[k] = v
	}
	for k, v := range override {
		args[k] = v
	}
	req.Params.Arguments = args
	res, err := tool.Handler(ctx, req)
	if err != nil {
		t.Fatalf("%s: handler error: %v", name, err)
	}
	return res
}

func mutatingRequests(rc *requestCounter) []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var out []string
	for _, p := range rc.paths {
		// nodestructure and datapreview are POSTs that only read.
		if strings.Contains(p, "/repository/nodestructure") || strings.Contains(p, "/datapreview/") {
			continue
		}
		if strings.HasPrefix(p, "POST ") || strings.HasPrefix(p, "PUT ") || strings.HasPrefix(p, "DELETE ") {
			out = append(out, p)
		}
	}
	return out
}

func TestReadOnlyInvariant_EveryToolIsClassified(t *testing.T) {
	s, _, _ := newReadOnlyTestServer(t, func(c *Config) { c.Mode = "expert" })
	classified := map[string]string{}
	for _, n := range readOnlyWriteTools {
		if classified[n] != "" {
			t.Errorf("%s listed twice", n)
		}
		classified[n] = "write"
	}
	for _, n := range readOnlyAllowedTools {
		if classified[n] != "" {
			t.Errorf("%s is in both lists (%s)", n, classified[n])
		}
		classified[n] = "allowed"
	}
	registered := s.mcpServer.ListTools()
	var unclassified, stale []string
	for n := range registered {
		if classified[n] == "" {
			unclassified = append(unclassified, n)
		}
	}
	for n := range classified {
		if _, ok := registered[n]; !ok {
			stale = append(stale, n)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	if len(unclassified) > 0 {
		t.Errorf("classify the new tool(s) %v: write tools need a refuseUnderSafety gate and an entry in readOnlyWriteTools", unclassified)
	}
	if len(stale) > 0 {
		t.Errorf("classified but no longer registered: %v", stale)
	}
}

func TestReadOnlyInvariant_WriteToolsSendNothingToSAP(t *testing.T) {
	for _, name := range readOnlyWriteTools {
		t.Run(name, func(t *testing.T) {
			// Transports enabled, so it is --read-only that refuses and not "not enabled".
			s, rc, gw := newReadOnlyTestServer(t, func(c *Config) { c.Mode = "expert"; c.ReadOnly = true; c.EnableTransports = true })
			res := invariantCall(t, s, name, nil)
			if !res.IsError {
				t.Errorf("%s succeeded under --read-only", name)
			}
			// The refusal must be the policy's, not a parameter error that would
			// disappear with better arguments.
			if text := resultText(res); !strings.Contains(text, "blocked by safety configuration") {
				t.Errorf("%s failed under --read-only but not with a policy refusal: %s", name, text)
			}
			if m := mutatingRequests(rc); len(m) > 0 {
				t.Errorf("%s sent mutating request(s) under --read-only: %v", name, m)
			}
			if n := gw.settled(); n != 0 {
				t.Errorf("%s opened %d RFC connection(s) under --read-only", name, n)
			}
		})
	}
}

// Control: the same calls without --read-only do reach SAP, so the test above
// is not passing merely because the arguments were rejected up front.
func TestReadOnlyInvariant_ControlWritesReachSAPWithoutReadOnly(t *testing.T) {
	for _, name := range []string{"UpdateSource", "DeleteObject", "CreateObject", "LockObject", "CreateTransport"} {
		t.Run(name, func(t *testing.T) {
			s, rc, _ := newReadOnlyTestServer(t, func(c *Config) { c.Mode = "expert"; c.EnableTransports = true })
			// A local package and no request: nothing else stands in the way.
			_ = invariantCall(t, s, name, map[string]any{"transport": "", "package": "$TMP", "package_name": "$TMP", "object_type": "PROG/P"})
			if len(mutatingRequests(rc)) == 0 {
				t.Errorf("%s sent no mutating request even without --read-only; the invariant test would pass vacuously for it", name)
			}
		})
	}
}

// The dry run stays usable on a read-only system.
func TestReadOnlyInvariant_DryRunStaysAllowed(t *testing.T) {
	s, rc, _ := newReadOnlyTestServer(t, func(c *Config) { c.Mode = "expert"; c.ReadOnly = true })
	tool := s.mcpServer.ListTools()["DeployZip"]
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"source": "abapgit-standalone", "package": "$ZGIT", "dry_run": true}
	res, err := tool.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("DeployZip dry_run: %v", err)
	}
	// The embedded ZIP may be a stub in a test build; what matters is that the
	// policy did not refuse a call that changes nothing.
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "blocked by safety configuration") {
			t.Fatalf("DeployZip dry_run refused under --read-only: %s", tc.Text)
		}
	}
	if rc.count() != 0 {
		t.Errorf("DeployZip dry_run sent %d request(s)", rc.count())
	}
}

func resultText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// The allowed tools are exercised too: a tool filed as harmless that in fact
// writes would otherwise pass unseen. Debugger and breakpoint traffic is
// allowed on purpose (2ap), and UnlockObject only releases.
func TestReadOnlyInvariant_AllowedToolsDoNotMutate(t *testing.T) {
	skip := map[string]bool{"UnlockObject": true}
	for _, name := range readOnlyAllowedTools {
		// The debugger tools need a live session (they panic without one) and are
		// allowed on a read-only system by design.
		if skip[name] || strings.HasPrefix(name, "Debugger") || strings.HasPrefix(name, "AMDP") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			s, rc, _ := newReadOnlyTestServer(t, func(c *Config) { c.Mode = "expert"; c.ReadOnly = true; c.EnableTransports = true })
			_ = invariantCall(t, s, name, nil)
			var bad []string
			for _, p := range mutatingRequests(rc) {
				if strings.Contains(p, "/debugger") || strings.Contains(p, "/amdp") || isReadOnlyPost(p) {
					continue
				}
				bad = append(bad, p)
			}
			if len(bad) > 0 {
				t.Errorf("%s is classified as harmless but sent mutating request(s) under --read-only: %v", name, bad)
			}
		})
	}
}

// isReadOnlyPost lists ADT POST endpoints that compute and return something
// without changing the system: where-used, unit-test runs (dangerous ones are
// gated separately, 2ap), the pretty printer and syntax/check runs.
func isReadOnlyPost(p string) bool {
	for _, frag := range []string{"/informationsystem/usageReferences", "/abapunit/testruns", "/abapsource/prettyprinter", "/checkruns"} {
		if strings.Contains(p, frag) {
			return true
		}
	}
	return false
}
