package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The package whitelist for the two ZADT_VSP WebSocket writes that have no ADT
// URL of their own: SET_TEXT_ELEMENTS (program package) and MOVE (source
// package). The stub answers the quick search with the hits given; every
// refusal test has a control that lets the same call reach the connection.

type searchHit struct{ objType, name, pkg string }

func newSearchTestServer(t *testing.T, hits []searchHit) (*Server, *requestCounter) {
	t.Helper()
	rc := &requestCounter{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.mu.Lock()
		rc.paths = append(rc.paths, r.Method+" "+r.URL.Path)
		rc.mu.Unlock()
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if strings.Contains(r.URL.Path, "informationsystem/search") {
			var sb strings.Builder
			sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`)
			for _, h := range hits {
				fmt.Fprintf(&sb, `<adtcore:objectReference adtcore:uri="/sap/bc/adt/x/%s" adtcore:type="%s" adtcore:name="%s" adtcore:packageName="%s"/>`,
					strings.ToLower(h.name), h.objType, h.name, h.pkg)
			}
			sb.WriteString(`</adtcore:objectReferences>`)
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(sb.String()))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	cfg := &Config{
		BaseURL: ts.URL, Username: "testuser", Password: "testpass", Client: "001", Language: "EN",
		Mode: "hyperfocused", AllowedPackages: []string{"Z*", "$TMP"},
	}
	server := NewServer(cfg)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	return server, rc
}

// searches counts the quick-search requests; everything else is what came
// after the gate (the WebSocket upgrade attempt).
func (c *requestCounter) searches() (search, other int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.paths {
		if strings.Contains(p, "informationsystem/search") {
			search++
		} else if !strings.Contains(p, "/core/discovery") {
			other++
		}
	}
	return
}

func setTextArgs() map[string]any {
	return map[string]any{"action": "debug", "target": "SET_TEXT_ELEMENTS",
		"params": map[string]any{"program": "ZDEMO", "selection_texts": `{"P_X":"x"}`}}
}

func moveArgs() map[string]any {
	return map[string]any{"action": "debug", "target": "MOVE",
		"params": map[string]any{"object_type": "PROG", "object_name": "ZDEMO", "new_package": "ZOTHER"}}
}

func TestSetTextElements_PackageOutsideWhitelistRefused(t *testing.T) {
	server, rc := newSearchTestServer(t, []searchHit{{"PROG/P", "ZDEMO", "SAP_STD"}})
	out := callSAP(t, server, setTextArgs())
	assertBlocked(t, out)
	if _, other := rc.searches(); other != 0 {
		t.Errorf("a refused SetTextElements sent %d request(s) after the search", other)
	}

	// Control: a package inside the whitelist goes on to the connection.
	server, rc = newSearchTestServer(t, []searchHit{{"PROG/P", "ZDEMO", "ZPKG"}})
	out = callSAP(t, server, setTextArgs())
	if strings.Contains(out, "blocked by safety configuration") {
		t.Fatalf("an allowed package must not be refused: %s", out)
	}
	if _, other := rc.searches(); other == 0 {
		t.Error("an allowed package should reach the WebSocket connection")
	}
}

func TestSetTextElements_ProgramNotFoundFailsClosed(t *testing.T) {
	server, rc := newSearchTestServer(t, nil)
	out := callSAP(t, server, setTextArgs())
	if !strings.Contains(out, "refused") {
		t.Errorf("expected a refusal for a program that cannot be found, got: %s", out)
	}
	if _, other := rc.searches(); other != 0 {
		t.Errorf("a refusal sent %d request(s) after the search", other)
	}
}

func TestSetTextElements_NoWhitelistNoLookup(t *testing.T) {
	server, rc := newSearchTestServer(t, nil)
	server.adtClient.Safety().AllowedPackages = nil
	_ = callSAP(t, server, setTextArgs())
	if s, _ := rc.searches(); s != 0 {
		t.Errorf("without a whitelist %d search request(s) were sent", s)
	}
}

func TestMoveObject_SourcePackageHonoursAllowedPackages(t *testing.T) {
	server, rc := newSearchTestServer(t, []searchHit{{"PROG/P", "ZDEMO", "SAP_STD"}})
	out := callSAP(t, server, moveArgs())
	assertBlocked(t, out)
	if _, other := rc.searches(); other != 0 {
		t.Errorf("a move out of a forbidden package sent %d request(s) after the search", other)
	}

	// Control: the destination is allowed too; only the source differs.
	server, rc = newSearchTestServer(t, []searchHit{{"PROG/P", "ZDEMO", "ZPKG"}})
	out = callSAP(t, server, moveArgs())
	if strings.Contains(out, "blocked by safety configuration") {
		t.Fatalf("a move out of an allowed package must not be refused: %s", out)
	}
	if _, other := rc.searches(); other == 0 {
		t.Error("a move out of an allowed package should reach the WebSocket connection")
	}
}

func TestMoveObject_SourceTypeMismatchFailsClosed(t *testing.T) {
	// Only a class of that name exists; the program asked for is not found.
	server, rc := newSearchTestServer(t, []searchHit{{"CLAS/OC", "ZDEMO", "ZPKG"}})
	out := callSAP(t, server, moveArgs())
	if !strings.Contains(out, "refused") {
		t.Errorf("expected a refusal when the type does not match, got: %s", out)
	}
	if _, other := rc.searches(); other != 0 {
		t.Errorf("a refusal sent %d request(s) after the search", other)
	}
}
