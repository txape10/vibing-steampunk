package adt

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestCheckMutation_NoPolicy_Passes(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{
		responses: map[string]*http.Response{"discovery": newTestResponse("OK")},
	}))

	err := client.checkMutation(context.Background(), MutationContext{
		Op:        OpUpdate,
		OpName:    "TestOp",
		ObjectURL: "/sap/bc/adt/programs/programs/ZTEST",
	})
	if err != nil {
		t.Fatalf("expected no error when no policy configured, got: %v", err)
	}
}

func TestCheckMutation_OpType_Blocked(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithReadOnly())
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{
		responses: map[string]*http.Response{"discovery": newTestResponse("OK")},
	}))

	err := client.checkMutation(context.Background(), MutationContext{
		Op:     OpUpdate,
		OpName: "TestOp",
	})
	if err == nil {
		t.Fatal("expected read-only mode to block OpUpdate")
	}
}

func TestCheckMutation_ExplicitPackage_NotInWhitelist(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowedPackages("$TMP"))
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{
		responses: map[string]*http.Response{"discovery": newTestResponse("OK")},
	}))

	err := client.checkMutation(context.Background(), MutationContext{
		Op:      OpCreate,
		OpName:  "CreateObject",
		Package: "ZOTHER",
	})
	if err == nil {
		t.Fatal("expected explicit package outside whitelist to be blocked")
	}
	if !strings.Contains(err.Error(), "ZOTHER") {
		t.Fatalf("expected error to mention blocked package, got: %v", err)
	}
}

func TestCheckMutation_ObjectURL_ResolvesADTPackage(t *testing.T) {
	mock := &mockTransportClient{
		responses: map[string]*http.Response{
			"search":    newSearchResponse("/sap/bc/adt/programs/programs/ztest", "PROG/P", "ZTEST", "ZOTHER"),
			"discovery": newTestResponse("OK"),
		},
	}
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowedPackages("$TMP"))
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	err := client.checkMutation(context.Background(), MutationContext{
		Op:        OpUpdate,
		OpName:    "UpdateSource",
		ObjectURL: "/sap/bc/adt/programs/programs/ZTEST/source/main",
	})
	if err == nil {
		t.Fatal("expected object URL resolution to block non-whitelisted package")
	}
	if !strings.Contains(err.Error(), "ZOTHER") {
		t.Fatalf("expected error to mention resolved package, got: %v", err)
	}
}

func TestCheckMutation_UI5Surface_BlockedWhenPolicyActive(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowedPackages("$TMP"))
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{
		responses: map[string]*http.Response{"discovery": newTestResponse("OK")},
	}))

	err := client.checkMutation(context.Background(), MutationContext{
		Op:        OpUpdate,
		OpName:    "UI5UploadFile",
		ObjectURL: "MYAPP",
		Surface:   SurfaceUI5,
	})
	if err == nil {
		t.Fatal("expected UI5 surface to be blocked until app→package resolution lands")
	}
	if !strings.Contains(err.Error(), "UI5") {
		t.Fatalf("expected error to mention UI5, got: %v", err)
	}
}

func TestCheckMutation_UI5Surface_AllowedWhenNoPolicy(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{
		responses: map[string]*http.Response{"discovery": newTestResponse("OK")},
	}))

	err := client.checkMutation(context.Background(), MutationContext{
		Op:        OpUpdate,
		OpName:    "UI5UploadFile",
		ObjectURL: "MYAPP",
		Surface:   SurfaceUI5,
	})
	if err != nil {
		t.Fatalf("expected UI5 surface to pass when no package policy, got: %v", err)
	}
}

func TestCheckMutation_MissingObjectURLAndPackage_FailsClosed(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowedPackages("$TMP"))
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{
		responses: map[string]*http.Response{"discovery": newTestResponse("OK")},
	}))

	err := client.checkMutation(context.Background(), MutationContext{
		Op:     OpUpdate,
		OpName: "MysteryOp",
	})
	if err == nil {
		t.Fatal("expected gate to fail closed when neither ObjectURL nor Package is provided under policy")
	}
	if !strings.Contains(err.Error(), "MysteryOp") {
		t.Fatalf("expected error to mention op name, got: %v", err)
	}
}

func TestClient_UI5UploadFile_BlockedUnderAllowedPackages(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowedPackages("$TMP"))
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{
		responses: map[string]*http.Response{"discovery": newTestResponse("OK")},
	}))

	err := client.UI5UploadFile(context.Background(), "MYAPP", "/index.html", []byte("x"), "text/html")
	if err == nil {
		t.Fatal("expected UI5UploadFile to be blocked under AllowedPackages policy")
	}
}

func TestClient_UI5DeleteFile_BlockedUnderAllowedPackages(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowedPackages("$TMP"))
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{
		responses: map[string]*http.Response{"discovery": newTestResponse("OK")},
	}))

	err := client.UI5DeleteFile(context.Background(), "MYAPP", "/index.html")
	if err == nil {
		t.Fatal("expected UI5DeleteFile to be blocked under AllowedPackages policy")
	}
}

func TestClient_UI5DeleteApp_BlockedUnderAllowedPackages(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowedPackages("$TMP"))
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{
		responses: map[string]*http.Response{"discovery": newTestResponse("OK")},
	}))

	err := client.UI5DeleteApp(context.Background(), "MYAPP", "")
	if err == nil {
		t.Fatal("expected UI5DeleteApp to be blocked under AllowedPackages policy")
	}
}

// --- Issue #143: WriteSource update path must self-resolve the package ---

func TestWriteSourceObjectURL_ResolvesAllSupportedTypes(t *testing.T) {
	tests := []struct {
		objectType string
		name       string
		parent     string
		want       string
	}{
		{"PROG", "ZTEST", "", "/sap/bc/adt/programs/programs/ZTEST"},
		{"CLAS", "ZCL_TEST", "", "/sap/bc/adt/oo/classes/ZCL_TEST"},
		{"INTF", "ZIF_TEST", "", "/sap/bc/adt/oo/interfaces/ZIF_TEST"},
		{"INCL", "ZTEST_F01", "", "/sap/bc/adt/programs/includes/ZTEST_F01"},
		{"DDLS", "ZTEST_CDS", "", "/sap/bc/adt/ddic/ddl/sources/ztest_cds"},
		{"BDEF", "ZTEST_BDEF", "", "/sap/bc/adt/bo/behaviordefinitions/ztest_bdef"},
		{"SRVD", "ZTEST_SRVD", "", "/sap/bc/adt/ddic/srvd/sources/ztest_srvd"},
		{"SRVB", "ZTEST_SRVB", "", "/sap/bc/adt/businessservices/bindings/ztest_srvb"},
		{"FUNC", "Z_TEST_FM", "ZFUGR", "/sap/bc/adt/functions/groups/ZFUGR/fmodules/Z_TEST_FM"},
		{"BOGUS", "ZX", "", ""},
	}

	for _, tt := range tests {
		if got := writeSourceObjectURL(tt.objectType, tt.name, tt.parent); got != tt.want {
			t.Errorf("writeSourceObjectURL(%q, %q, %q) = %q, want %q", tt.objectType, tt.name, tt.parent, got, tt.want)
		}
	}
}

func TestClient_WriteSource_Update_AllowedPackages_Allowed(t *testing.T) {
	mock := &mockTransportClient{
		responses: map[string]*http.Response{
			"search":    newSearchResponse("/sap/bc/adt/programs/programs/ztest", "PROG/P", "ZTEST", "ZDEMO"),
			"discovery": newDiscoveryOKResponse(),
			"checkruns": newTestResponse(cleanSyntaxCheckXML),
			// parseActivationResult treats an empty body as a successful activation;
			// a non-empty non-XML body like "OK" is parsed as an activation error.
			"activation": newTestResponse(""),
			"/sap/bc/adt/programs/programs/ZTEST/source/main": newTestResponse("REPORT ztest."),
			"/sap/bc/adt/programs/programs/ZTEST":             newTestResponse(lockResponseXML),
		},
	}
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass",
		WithAllowedPackages("Z*"),
		WithAllowTransportableEdits(), // lock response carries a real corrNr
	)
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	// opts.Package is intentionally empty, as it would be for a real update
	// call — the gate must resolve the package itself instead of failing
	// closed with "requires either ObjectURL or Package". The GetProgram
	// mock above makes the object resolve as existing, so the (separately
	// fixed) existence check doesn't reject this as "does not exist".
	result, err := client.WriteSource(context.Background(), "PROG", "ZTEST", "REPORT ztest.", &WriteSourceOptions{
		Mode: WriteModeUpdate,
	})
	if err != nil {
		t.Fatalf("expected WriteSource update to pass the mutation gate via resolved package, got: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected WriteSource update to succeed, got message: %q", result.Message)
	}
}

func TestClient_WriteSource_Update_AllowedPackages_Blocked(t *testing.T) {
	mock := &mockTransportClient{
		responses: map[string]*http.Response{
			"search":    newSearchResponse("/sap/bc/adt/programs/programs/ztest", "PROG/P", "ZTEST", "ZOTHER"),
			"discovery": newTestResponse("OK"),
		},
	}
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowedPackages("$TMP"))
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock))

	_, err := client.WriteSource(context.Background(), "PROG", "ZTEST", "REPORT ztest.", &WriteSourceOptions{
		Mode: WriteModeUpdate,
	})
	if err == nil {
		t.Fatal("expected WriteSource update to be blocked for a package outside the whitelist")
	}
	if !strings.Contains(err.Error(), "ZOTHER") {
		t.Fatalf("expected error to mention the resolved package, got: %v", err)
	}
}

// --- Issue #144 follow-up: transport adoption must re-validate policy ---
//
// WriteProgram/WriteInclude/WriteClass/EditSourceWithOptions/DeleteObjectWithAutoLock
// adopt LockResult.CorrNr as the effective transport when the caller supplies none.
// The top-level mutation gate only ever saw an empty transport at that point (the
// object's existing request isn't known until after the lock), so without a
// re-check here, a disabled AllowTransportableEdits policy would be silently
// bypassed whenever the object happened to already sit in an open request.

func TestResolveWriteTransport_SuppliedTransport_UsedAsIs(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass") // AllowTransportableEdits defaults to false
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{}))

	got, err := client.resolveWriteTransport("D15K000001", "D99K999999", "TestOp")
	if err != nil {
		t.Fatalf("expected explicit transport to bypass policy re-check, got: %v", err)
	}
	if got != "D15K000001" {
		t.Errorf("expected supplied transport to win, got %q", got)
	}
}

func TestResolveWriteTransport_NoSuppliedNoLock_StaysLocal(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{}))

	got, err := client.resolveWriteTransport("", "", "TestOp")
	if err != nil {
		t.Fatalf("expected local object (no transport anywhere) to pass, got: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty transport to stay empty, got %q", got)
	}
}

func TestResolveWriteTransport_AdoptFromLock_AllowedWhenTransportableEditsEnabled(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithAllowTransportableEdits())
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{}))

	got, err := client.resolveWriteTransport("", "D15K000001", "TestOp")
	if err != nil {
		t.Fatalf("expected adoption to succeed when transportable edits are allowed, got: %v", err)
	}
	if got != "D15K000001" {
		t.Errorf("expected lock CorrNr to be adopted, got %q", got)
	}
}

func TestResolveWriteTransport_AdoptFromLock_BlockedWhenTransportableEditsDisabled(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass") // AllowTransportableEdits defaults to false
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{}))

	got, err := client.resolveWriteTransport("", "D15K000001", "TestOp")
	if err == nil {
		t.Fatal("expected adopting a transport to be blocked when AllowTransportableEdits is disabled")
	}
	if got != "" {
		t.Errorf("expected empty result on blocked adoption, got %q", got)
	}
	if !strings.Contains(err.Error(), "TestOp") {
		t.Errorf("expected error to mention the op name, got: %v", err)
	}
}

func TestResolveWriteTransport_AdoptFromLock_RespectsTransportWhitelist(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass",
		WithAllowTransportableEdits(), WithAllowedTransports("D15K*"))
	client := NewClientWithTransport(cfg, NewTransportWithClient(cfg, &mockTransportClient{}))

	if got, err := client.resolveWriteTransport("", "D15K000001", "TestOp"); err != nil || got != "D15K000001" {
		t.Errorf("expected whitelisted transport to be adopted, got (%q, %v)", got, err)
	}

	got, err := client.resolveWriteTransport("", "D99K999999", "TestOp")
	if err == nil {
		t.Fatal("expected adoption of a non-whitelisted transport to be blocked")
	}
	if got != "" {
		t.Errorf("expected empty result on blocked adoption, got %q", got)
	}
}
