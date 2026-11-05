package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// newWriteCountingServer answers the TADIR read with rows and counts every
// request that could create, change or delete something.
func newWriteCountingServer(t *testing.T, rows [][]string) (*Server, *int32) {
	t.Helper()
	var writes int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if strings.HasSuffix(r.URL.Path, "/datapreview/freestyle") {
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, tadirDP(rows))
			return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			atomic.AddInt32(&writes, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	s := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN",
		Mode: "hyperfocused", AllowedPackages: []string{"Z*", "$TMP"}, AllowTransportableEdits: true})
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	return s, &writes
}

func callArgs(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

func TestCreateObject_StaleRowWithoutTransportRefusesBeforeAnyWrite(t *testing.T) {
	s, writes := newWriteCountingServer(t, [][]string{{"ZABAP01", "X"}})
	res, err := s.handleCreateObject(context.Background(), callArgs(map[string]any{
		"object_type": "PROG/P", "name": "ZVSP_X", "description": "d", "package_name": "ZSU01",
	}))
	if err != nil || res == nil || !res.IsError || !strings.Contains(resultText(res), "Nothing was created") {
		t.Fatalf("expected a refusal, got %v %v", res, err)
	}
	if n := atomic.LoadInt32(writes); n != 0 {
		t.Errorf("a refused create must send no write, sent %d", n)
	}
}

func TestCreateObject_NotApplicableReadsNothing(t *testing.T) {
	// A local package and a non-movable type must not even read TADIR.
	s, reads := newTADIRServer(t, [][]string{{"ZABAP01", "X"}}, false)
	for _, c := range []struct{ typ, pkg string }{{"PROG/P", "$TMP"}, {"FUGR/F", "ZSU01"}, {"DDLS/DF", "ZSU01"}} {
		if pc, refused := s.planPackageCheck(context.Background(), c.typ, "ZX", c.pkg, "", "create"); pc != nil || refused != nil {
			t.Errorf("%+v must not apply", c)
		}
	}
	if *reads != 0 {
		t.Errorf("read TADIR %d times", *reads)
	}
}

func TestDeployFromFile_StaleRowWithoutTransportRefusesBeforeAnyWrite(t *testing.T) {
	s, writes := newWriteCountingServer(t, [][]string{{"ZABAP01", "X"}})
	dir := t.TempDir()
	file := filepath.Join(dir, "zvsp_x.prog.abap")
	if err := os.WriteFile(file, []byte("REPORT zvsp_x.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.deployFromFile(context.Background(), callArgs(map[string]any{
		"file_path": file, "package_name": "ZSU01",
	}))
	if err != nil || res == nil || !res.IsError || !strings.Contains(resultText(res), "Nothing was created") {
		t.Fatalf("expected a refusal, got %v %v", res, err)
	}
	if n := atomic.LoadInt32(writes); n != 0 {
		t.Errorf("a refused deploy must send no write, sent %d", n)
	}
}

func TestRenameObject_StaleRowWithoutTransportRefusesBeforeAnyWrite(t *testing.T) {
	s, writes := newWriteCountingServer(t, [][]string{{"ZABAP01", "X"}})
	res, err := s.handleRenameObject(context.Background(), callArgs(map[string]any{
		"objType": "PROG/P", "oldName": "ZVSP_OLD", "newName": "ZVSP_NEW", "packageName": "ZSU01",
	}))
	if err != nil || res == nil || !res.IsError || !strings.Contains(resultText(res), "Nothing was created") {
		t.Fatalf("expected a refusal, got %v %v", res, err)
	}
	if n := atomic.LoadInt32(writes); n != 0 {
		t.Errorf("a refused rename must send no write, sent %d", n)
	}
}

func TestCheckPackageAfterCreate_ShellTextAndBudgetExpired(t *testing.T) {
	pc := &packageCheck{objType: "PROG", name: "ZX", pkg: "ZSU01", transport: testRequest, created: "created as an empty inactive shell"}

	// Wrong package, no move possible: the text says what exists (a shell).
	s, _ := newTADIRServer(t, [][]string{{"ZABAP01", ""}}, false)
	s.moveFn = func(context.Context, string, string, string, string, bool) (*adt.MoveObjectResult, error) {
		return &adt.MoveObjectResult{Success: false, Code: "X", Message: "refused"}, nil
	}
	text, st := s.checkPackageAfterCreate(context.Background(), pc)
	if st != pkgMismatch || !strings.Contains(text, "created as an empty inactive shell") || !strings.Contains(text, "Nothing was deleted") {
		t.Errorf("shell mismatch text: %v %q", st, text)
	}

	// The call's budget already expired: TADIR is still read, but no move is tried.
	s, _ = newTADIRServer(t, [][]string{{"ZABAP01", ""}}, false)
	moves := 0
	s.moveFn = func(context.Context, string, string, string, string, bool) (*adt.MoveObjectResult, error) {
		moves++
		return &adt.MoveObjectResult{Success: true, Recording: "dry_run"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	text, st = s.checkPackageAfterCreate(ctx, pc)
	if st != pkgMismatch || moves != 0 || !strings.Contains(text, "budget expired") {
		t.Errorf("expired budget must not move: st=%v moves=%d %q", st, moves, text)
	}
}

func TestReportPackageOnly_NeverMoves(t *testing.T) {
	pc := &packageCheck{objType: "PROG", name: "ZX", pkg: "ZSU01", transport: testRequest}
	cases := []struct {
		name string
		rows [][]string
		fail bool
		want string
	}{
		{"wrong package", [][]string{{"ZABAP01", ""}}, false, "WARNING"},
		{"right package", [][]string{{"ZSU01", ""}}, false, "nothing was moved"},
		{"no row", nil, false, "not verified"},
		{"unreadable", nil, true, "not verified"},
	}
	for _, c := range cases {
		s, _ := newTADIRServer(t, c.rows, c.fail)
		s.moveFn = func(context.Context, string, string, string, string, bool) (*adt.MoveObjectResult, error) {
			t.Errorf("%s: reportPackageOnly must never move", c.name)
			return nil, nil
		}
		if got := s.reportPackageOnly(context.Background(), pc); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q lacks %q", c.name, got, c.want)
		}
	}
}

func TestApplyDeployPackageCheck(t *testing.T) {
	pc := &packageCheck{objType: "PROG", name: "ZX", pkg: "ZSU01", stale: "ZABAP01"}
	noMove := func(t *testing.T, s *Server) {
		s.moveFn = func(context.Context, string, string, string, string, bool) (*adt.MoveObjectResult, error) {
			t.Error("this branch must not move")
			return nil, nil
		}
	}

	// Created and complete, right package: ok.
	s, _ := newTADIRServer(t, [][]string{{"ZSU01", ""}}, false)
	r := &adt.DeployResult{ObjectCreated: true, Success: true}
	if s.applyDeployPackageCheck(context.Background(), pc, r) || !strings.HasPrefix(r.PackageCheck, "ok") {
		t.Errorf("ok case: %q", r.PackageCheck)
	}

	// Created and complete, wrong package, no transport: MISMATCH, nothing deleted.
	s, _ = newTADIRServer(t, [][]string{{"ZABAP01", ""}}, false)
	r = &adt.DeployResult{ObjectCreated: true, Success: true}
	if !s.applyDeployPackageCheck(context.Background(), pc, r) || !strings.Contains(r.PackageCheck, "MISMATCH") {
		t.Errorf("mismatch case: %q", r.PackageCheck)
	}

	// Failed after creating it: only reports, never moves.
	s, _ = newTADIRServer(t, [][]string{{"ZABAP01", ""}}, false)
	noMove(t, s)
	pcT := *pc
	pcT.transport = testRequest
	r = &adt.DeployResult{ObjectCreated: true, Success: false}
	if s.applyDeployPackageCheck(context.Background(), &pcT, r) || !strings.Contains(r.PackageCheck, "WARNING") {
		t.Errorf("partial case: %q", r.PackageCheck)
	}

	// The create call failed: only the stale-row note; an update (success, not created) adds nothing.
	s, reads := newTADIRServer(t, nil, false)
	r = &adt.DeployResult{Success: false}
	s.applyDeployPackageCheck(context.Background(), pc, r)
	if !strings.Contains(r.PackageCheck, "ZABAP01") {
		t.Errorf("create-failed note: %q", r.PackageCheck)
	}
	r = &adt.DeployResult{Success: true}
	s.applyDeployPackageCheck(context.Background(), pc, r)
	if r.PackageCheck != "" || *reads != 0 {
		t.Errorf("an update must add nothing and read nothing: %q reads=%d", r.PackageCheck, *reads)
	}
	if s.applyDeployPackageCheck(context.Background(), nil, r) {
		t.Error("nil pc must be a no-op")
	}
}

func TestRenameVerifier_KeepsOldUnlessPackageIsRight(t *testing.T) {
	pc := &packageCheck{objType: "PROG", name: "ZX", pkg: "ZSU01"}
	cases := []struct {
		name string
		rows [][]string
		fail bool
		ok   bool
	}{
		{"right package", [][]string{{"ZSU01", ""}}, false, true},
		{"wrong package", [][]string{{"ZABAP01", ""}}, false, false},
		{"TADIR unreadable", nil, true, false},
		{"no row", nil, false, false},
	}
	for _, c := range cases {
		s, _ := newTADIRServer(t, c.rows, c.fail)
		ok, note := s.renameVerifier(pc)(context.Background())
		if ok != c.ok || note == "" {
			t.Errorf("%s: ok=%v note=%q, want ok=%v", c.name, ok, note, c.ok)
		}
	}
}
