package adt

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// dpXML builds a data preview answer: one <columns> element per column.
func dpXML(cols []string, rows [][]string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">`)
	for i, col := range cols {
		sb.WriteString(`<dataPreview:columns><dataPreview:metadata dataPreview:name="` + col + `" dataPreview:type="C" dataPreview:length="30"/><dataPreview:dataSet>`)
		for _, row := range rows {
			sb.WriteString(`<dataPreview:data>` + html.EscapeString(row[i]) + `</dataPreview:data>`)
		}
		sb.WriteString(`</dataPreview:dataSet></dataPreview:columns>`)
	}
	sb.WriteString(`</dataPreview:tableData>`)
	return sb.String()
}

const inventoryNodeStructure = `<?xml version="1.0" encoding="UTF-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA><TREE_CONTENT>` +
	`<SEU_ADT_REPOSITORY_OBJ_NODE><OBJECT_TYPE>CLAS/OC</OBJECT_TYPE><OBJECT_NAME>ZCL_B</OBJECT_NAME><DESCRIPTION>class b</DESCRIPTION></SEU_ADT_REPOSITORY_OBJ_NODE>` +
	`<SEU_ADT_REPOSITORY_OBJ_NODE><OBJECT_TYPE>DEVC/K</OBJECT_TYPE><OBJECT_NAME>ZDEMO_SUB</OBJECT_NAME></SEU_ADT_REPOSITORY_OBJ_NODE>` +
	`</TREE_CONTENT></DATA></asx:values></asx:abap>`

const abapGitRepoXML = `<?xml version="1.0" encoding="utf-16"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>` +
	`<URL>https://example.local/repo.git</URL><PACKAGE>ZDEMO</PACKAGE><BRANCH_NAME>refs/heads/main</BRANCH_NAME><OFFLINE>X</OFFLINE><CREATED_BY>TESTUSER</CREATED_BY>` +
	`</DATA></asx:values></asx:abap>`

type inventoryStub struct {
	mu        sync.Mutex
	queries   []string
	nodeHits  int
	noAbapGit bool
	failTadir bool
	tadirRows [][]string
}

func (s *inventoryStub) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-CSRF-Token", "TOKEN")
	switch {
	case strings.HasSuffix(r.URL.Path, "/datapreview/freestyle"):
		raw, _ := io.ReadAll(r.Body)
		q := string(raw)
		s.mu.Lock()
		s.queries = append(s.queries, q)
		s.mu.Unlock()
		lower := strings.ToLower(q)
		switch {
		case strings.Contains(lower, "from tadir"):
			if s.failTadir {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, dpXML([]string{"OBJECT", "OBJ_NAME", "AUTHOR", "CREATED_ON"}, s.tadirRows))
		case strings.Contains(lower, "from tdevc"):
			_, _ = io.WriteString(w, dpXML([]string{"DEVCLASS", "AS4USER", "CREATED_ON"}, [][]string{{"ZDEMO_SUB", "TESTUSER", "20240131"}}))
		case strings.Contains(lower, "from dd02l"):
			rows := [][]string{{"ZABAPGIT"}}
			if s.noAbapGit {
				rows = nil
			}
			_, _ = io.WriteString(w, dpXML([]string{"TABNAME"}, rows))
		case strings.Contains(lower, "from zabapgit"):
			_, _ = io.WriteString(w, dpXML([]string{"VALUE", "DATA_STR"}, [][]string{
				{"000000000001", abapGitRepoXML},
				{"000000000002", strings.Replace(abapGitRepoXML, "<PACKAGE>ZDEMO</PACKAGE>", "<PACKAGE>ZOTHER</PACKAGE>", 1)},
			}))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	case strings.HasSuffix(r.URL.Path, "/repository/nodestructure"):
		s.mu.Lock()
		s.nodeHits++
		s.mu.Unlock()
		_, _ = io.WriteString(w, inventoryNodeStructure)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func newInventoryClient(t *testing.T, s *inventoryStub, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "u", "p", opts...)
}

func TestPackageInventory_ReadsTadirTdevcAndAbapGit(t *testing.T) {
	s := &inventoryStub{tadirRows: [][]string{
		{"PROG", "ZB_PROG", "TESTUSER", "20240115"},
		{"CLAS", "ZCL_A", "TESTUSER", "00000000"},
	}}
	c := newInventoryClient(t, s)

	inv, err := c.PackageInventory(context.Background(), " zdemo ")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Package != "ZDEMO" || len(inv.Objects) != 2 {
		t.Fatalf("got %+v", inv)
	}
	if inv.Objects[0].Type != "CLAS" || inv.Objects[0].CreatedOn != "" || inv.Objects[1].Name != "ZB_PROG" || inv.Objects[1].CreatedOn != "2024-01-15" {
		t.Fatalf("objects must be sorted by type and name, dates formatted: %+v", inv.Objects)
	}
	if len(inv.Subpackages) != 1 || inv.Subpackages[0].Name != "ZDEMO_SUB" || inv.Subpackages[0].CreatedOn != "2024-01-31" {
		t.Fatalf("subpackages: %+v", inv.Subpackages)
	}
	if len(inv.AbapGitRepos) != 1 || inv.AbapGitRepos[0].Package != "ZDEMO" || inv.AbapGitRepos[0].Branch != "refs/heads/main" || !inv.AbapGitRepos[0].Offline {
		t.Fatalf("only the repository of this package must be returned: %+v", inv.AbapGitRepos)
	}
	if s.nodeHits != 0 {
		t.Fatal("the ADT tree must not be read when TADIR and TDEVC answered")
	}
	if !strings.Contains(s.queries[0], "devclass = 'ZDEMO'") {
		t.Fatalf("package not in the TADIR query: %q", s.queries[0])
	}
}

func TestPackageInventory_RefusesInvalidPackageName(t *testing.T) {
	s := &inventoryStub{}
	c := newInventoryClient(t, s)
	for _, name := range []string{"", "ZX' OR devclass LIKE '", "Z X", strings.Repeat("Z", 31)} {
		if _, err := c.PackageInventory(context.Background(), name); err == nil {
			t.Errorf("%q must be refused", name)
		}
	}
	if len(s.queries) != 0 || s.nodeHits != 0 {
		t.Fatalf("a refused name must send nothing: %v", s.queries)
	}
	// Control: a namespaced name is valid.
	s.tadirRows = [][]string{}
	if _, err := c.PackageInventory(context.Background(), "/DMO/FLIGHT"); err != nil {
		t.Fatalf("namespaced package: %v", err)
	}
}

func TestPackageInventory_BlockFreeSQLReadsOnlyTheADTTree(t *testing.T) {
	s := &inventoryStub{}
	safety := UnrestrictedSafetyConfig()
	safety.BlockFreeSQL = true
	c := newInventoryClient(t, s, WithSafety(safety))

	inv, err := c.PackageInventory(context.Background(), "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.queries) != 0 {
		t.Fatalf("free SQL is blocked, yet queries were sent: %v", s.queries)
	}
	if len(inv.Objects) != 1 || inv.Objects[0].Type != "CLAS" || inv.Objects[0].Description != "class b" {
		t.Fatalf("objects from the tree: %+v", inv.Objects)
	}
	if len(inv.Subpackages) != 1 || len(inv.Skipped) != 3 || inv.AbapGitRepos != nil {
		t.Fatalf("what was skipped must be said, abapGit left unknown: %+v", inv)
	}
}

func TestPackageInventory_TadirFailureFallsBackToTheTree(t *testing.T) {
	s := &inventoryStub{failTadir: true}
	c := newInventoryClient(t, s)
	inv, err := c.PackageInventory(context.Background(), "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if s.nodeHits != 1 || len(inv.Objects) != 1 || inv.Objects[0].Name != "ZCL_B" {
		t.Fatalf("fallback: hits=%d objects=%+v", s.nodeHits, inv.Objects)
	}
	if len(inv.Notes) == 0 || !strings.Contains(inv.Notes[0], "TADIR could not be read") {
		t.Fatalf("the fallback must be noted: %v", inv.Notes)
	}
}

func TestPackageInventory_NoAbapGitTable(t *testing.T) {
	s := &inventoryStub{noAbapGit: true, tadirRows: [][]string{}}
	c := newInventoryClient(t, s)
	inv, err := c.PackageInventory(context.Background(), "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if inv.AbapGitRepos == nil || len(inv.AbapGitRepos) != 0 {
		t.Fatalf("no table means none registered, not unknown: %#v", inv.AbapGitRepos)
	}
	for _, q := range s.queries {
		if strings.Contains(strings.ToLower(q), "from zabapgit") {
			t.Fatal("ZABAPGIT must not be read when DD02L says it does not exist")
		}
	}
}

func TestPackageInventory_TruncatesAndSaysSo(t *testing.T) {
	old := inventoryMaxObjects
	inventoryMaxObjects = 2
	t.Cleanup(func() { inventoryMaxObjects = old })
	s := &inventoryStub{tadirRows: [][]string{
		{"PROG", "ZA", "U", "20240101"}, {"PROG", "ZB", "U", "20240101"}, {"PROG", "ZC", "U", "20240101"},
	}}
	c := newInventoryClient(t, s)
	inv, err := c.PackageInventory(context.Background(), "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if !inv.ObjectsTruncated || len(inv.Objects) != 2 {
		t.Fatalf("truncated=%v objects=%d", inv.ObjectsTruncated, len(inv.Objects))
	}
	// Exactly the limit is not truncation.
	s.tadirRows = s.tadirRows[:2]
	inv, err = c.PackageInventory(context.Background(), "ZDEMO")
	if err != nil || inv.ObjectsTruncated {
		t.Fatalf("exactly the limit must not be reported as cut: %v %+v", err, inv)
	}
}

func TestPackageInventory_CancelledContextIsAnError(t *testing.T) {
	s := &inventoryStub{}
	c := newInventoryClient(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.PackageInventory(ctx, "ZDEMO"); err == nil {
		t.Fatal("a dead context must not produce a partial inventory without error")
	}
}

func TestParseAbapGitRepo(t *testing.T) {
	repo, ok := parseAbapGitRepo(" 000000000001 ", abapGitRepoXML)
	if !ok || repo.Key != "000000000001" || repo.URL != "https://example.local/repo.git" || repo.CreatedBy != "TESTUSER" {
		t.Fatalf("got %+v %v", repo, ok)
	}
	if _, ok := parseAbapGitRepo("k", "<DATA></DATA>"); ok {
		t.Fatal("a row without a package is not a repository")
	}
}

func TestSapDate(t *testing.T) {
	for in, want := range map[string]string{"20251218": "2025-12-18", "00000000": "", "": "", "2025": "2025"} {
		if got := sapDate(in); got != want {
			t.Errorf("sapDate(%q) = %q, want %q", in, got, want)
		}
	}
}

// The inventory must see an object someone else just created: TADIR and
// TDEVC are not served from the response cache.
func TestStableTablesExcludePackageTables(t *testing.T) {
	for _, tab := range []string{"TADIR", "TDEVC"} {
		if stableTables[tab] {
			t.Errorf("%s must not be cacheable", tab)
		}
	}
	if stableQuery("SELECT object, obj_name FROM tadir WHERE devclass = 'ZDEMO'") {
		t.Error("a query on TADIR must not be kept in the response cache")
	}
}
