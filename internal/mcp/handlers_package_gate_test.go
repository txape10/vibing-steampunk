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
		"params": map[string]any{"object_type": "PROG", "object_name": "ZDEMO", "new_package": "$TMP"}}
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
	server, rc := newSearchTestServer(t, []searchHit{{"PROG/P", "ZDEMO", "$SAPLOCAL"}})
	out := callSAP(t, server, moveArgs())
	assertBlocked(t, out)
	if _, other := rc.searches(); other != 0 {
		t.Errorf("a move out of a forbidden package sent %d request(s) after the search", other)
	}

	// Control: the destination is allowed too; only the source differs.
	server, rc = newSearchTestServer(t, []searchHit{{"PROG/P", "ZDEMO", "$TMP"}})
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
	server, rc := newSearchTestServer(t, []searchHit{{"CLAS/OC", "ZDEMO", "$TMP"}})
	out := callSAP(t, server, moveArgs())
	if !strings.Contains(out, "refused") {
		t.Errorf("expected a refusal when the type does not match, got: %s", out)
	}
	if _, other := rc.searches(); other != 0 {
		t.Errorf("a refusal sent %d request(s) after the search", other)
	}
}

// moveWithTransport builds a MOVE call; an empty transport leaves it out.
func moveWithTransport(objType, name, pkg, transport string) map[string]any {
	params := map[string]any{"object_type": objType, "object_name": name, "new_package": pkg}
	if transport != "" {
		params["transport"] = transport
	}
	return map[string]any{"action": "debug", "target": "MOVE", "params": params}
}

// allowTransports lets the test server's policy accept a named request (the
// default policy refuses transportable edits).
func allowTransports(server *Server) {
	server.adtClient.Safety().AllowTransportableEdits = true
}

func TestMoveObject_RefusalsSendNothingAfterTheSearch(t *testing.T) {
	cases := []struct {
		name       string
		hits       []searchHit
		args       map[string]any
		want       string
		afterQuery bool // whether the refusal comes after the package search
	}{
		{"transportable destination without transport", []searchHit{{"PROG/P", "ZDEMO", "$TMP"}},
			moveWithTransport("PROG", "ZDEMO", "ZABAP01", ""), "give transport=", false},
		{"local destination with transport", []searchHit{{"PROG/P", "ZDEMO", "$TMP"}},
			moveWithTransport("PROG", "ZDEMO", "$TMP", "S4DK000001"), "local package", false},
		{"transport is not a request number", []searchHit{{"PROG/P", "ZDEMO", "$TMP"}},
			moveWithTransport("PROG", "ZDEMO", "ZABAP01", "S4DK12"), "not a transport request number", false},
		{"injection in transport", []searchHit{{"PROG/P", "ZDEMO", "$TMP"}},
			moveWithTransport("PROG", "ZDEMO", "ZABAP01", "S4DK000001' OR 1=1"), "not a transport request number", false},
		{"unsupported type with transport", []searchHit{{"FUGR/F", "ZDEMO", "$TMP"}},
			moveWithTransport("FUNC", "ZDEMO", "ZABAP01", "S4DK000001"), "only supports PROG, CLAS and INTF", false},
		{"transportable source to another transportable package", []searchHit{{"PROG/P", "ZDEMO", "ZOTHER"}},
			moveWithTransport("PROG", "ZDEMO", "ZABAP01", "S4DK000001"), "out of local", true},
		{"transportable source to a local package", []searchHit{{"PROG/P", "ZDEMO", "ZABAP01"}},
			moveWithTransport("PROG", "ZDEMO", "$TMP", ""), "out of local", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, rc := newSearchTestServer(t, tc.hits)
			allowTransports(server)
			out := callSAP(t, server, tc.args)
			if !strings.Contains(out, tc.want) {
				t.Errorf("expected %q in the refusal, got: %s", tc.want, out)
			}
			s, other := rc.searches()
			if other != 0 {
				t.Errorf("a refusal sent %d request(s) after the search", other)
			}
			if !tc.afterQuery && s != 0 {
				t.Errorf("this refusal must come before any search, but %d search(es) were sent", s)
			}
		})
	}
}

// The controls of the refusals above: the same calls, valid, reach the connection.
func TestMoveObject_ValidMovesReachTheConnection(t *testing.T) {
	cases := []struct {
		name string
		hits []searchHit
		args map[string]any
	}{
		{"local to local", []searchHit{{"PROG/P", "ZDEMO", "$TMP"}}, moveWithTransport("PROG", "ZDEMO", "$TMP", "")},
		{"local into transportable with a request", []searchHit{{"PROG/P", "ZDEMO", "$TMP"}},
			moveWithTransport("PROG", "ZDEMO", "ZABAP01", "S4DK000001")},
		{"lower-case request and class subtype", []searchHit{{"CLAS/OC", "ZCL_DEMO", "$TMP"}},
			moveWithTransport("clas/oc", "ZCL_DEMO", "ZABAP01", "s4dk000001")},
		{"already in the target: completes the recording", []searchHit{{"PROG/P", "ZDEMO", "ZABAP01"}},
			moveWithTransport("PROG", "ZDEMO", "ZABAP01", "S4DK000001")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, rc := newSearchTestServer(t, tc.hits)
			allowTransports(server)
			out := callSAP(t, server, tc.args)
			for _, refusal := range []string{"blocked by safety configuration", "out of local", "not a transport request number", "give transport="} {
				if strings.Contains(out, refusal) {
					t.Fatalf("a valid move must not be refused (%q): %s", refusal, out)
				}
			}
			if _, other := rc.searches(); other == 0 {
				t.Error("a valid move should reach the WebSocket connection")
			}
		})
	}
}

func TestMoveObject_TransportPolicyApplies(t *testing.T) {
	hits := []searchHit{{"PROG/P", "ZDEMO", "$TMP"}}
	args := moveWithTransport("PROG", "ZDEMO", "ZABAP01", "S4DK000001")

	// --allow-transportable-edits off: refused before anything is sent.
	server, rc := newSearchTestServer(t, hits)
	server.adtClient.Safety().AllowTransportableEdits = false
	out := callSAP(t, server, args)
	if !strings.Contains(strings.ToLower(out), "transport") {
		t.Errorf("expected a transport policy refusal, got: %s", out)
	}
	if s, other := rc.searches(); s != 0 || other != 0 {
		t.Errorf("a policy refusal sent %d search(es) and %d other request(s)", s, other)
	}

	// --allowed-transports without this request: refused too.
	server, rc = newSearchTestServer(t, hits)
	allowTransports(server)
	server.adtClient.Safety().AllowedTransports = []string{"S4DK999999"}
	out = callSAP(t, server, args)
	if !strings.Contains(out, "S4DK000001") {
		t.Errorf("expected the refusal to name the request, got: %s", out)
	}
	if s, other := rc.searches(); s != 0 || other != 0 {
		t.Errorf("a whitelist refusal sent %d search(es) and %d other request(s)", s, other)
	}
}

func TestMoveNotRecordedMessage_NamesTheRisk(t *testing.T) {
	msg := moveNotRecordedMessage("PROG", "ZX", "ZABAP01", "S4DK000001")
	for _, want := range []string{"WITHOUT being in any request", "ZABAP01", "S4DK000001", "ZX"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
}
