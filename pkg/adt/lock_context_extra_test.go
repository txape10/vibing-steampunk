package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// A stateless request that went out isolated while another caller's lock is
// outstanding must not drop the jar when SAP answers it with a session error:
// that would delete the sap-contextid the lock chain needs.
func TestLockContext_IsolatedSessionErrorKeepsTheLockChainsJar(t *testing.T) {
	s := &contextServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/expired" {
			http.Error(w, "ICMENOSESSION", http.StatusBadRequest)
			return
		}
		s.handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "TESTUSER", "pw")
	ctx := context.Background()

	if _, err := c.LockObject(ctx, "/sap/bc/adt/programs/programs/ZDEMO", "MODIFY"); err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	u, _ := url.Parse(srv.URL)
	jarBefore := c.transport.jar
	if _, err := c.transport.Request(ctx, "/sap/bc/adt/expired", nil); err == nil {
		t.Fatal("expected the session error to come back")
	}
	if c.transport.jar != jarBefore {
		t.Error("the jar was replaced: the lock chain's sap-contextid is gone")
	}
	found := false
	for _, ck := range c.transport.jar.Cookies(u) {
		if ck.Name == "sap-contextid" && ck.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("the stateful context id is no longer in the jar")
	}
}

// Both constructors give the transport the client's lock window.
func TestNewClientWithTransport_WiresTheLockWindow(t *testing.T) {
	cfg := NewConfig("http://example.invalid", "u", "p")
	c := NewClientWithTransport(cfg, NewTransport(cfg))
	if c.transport.locks != &c.locks {
		t.Error("NewClientWithTransport did not wire the lock window into the transport")
	}
	if c2 := NewClient("http://example.invalid", "u", "p"); c2.transport.locks != &c2.locks {
		t.Error("NewClient did not wire the lock window into the transport")
	}
}
