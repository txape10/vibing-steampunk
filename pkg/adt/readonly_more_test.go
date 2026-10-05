package adt

import (
	"net/http"
	"strings"
	"testing"
)

// readOnlyClient is a client under --read-only (plus whatever opts add) whose
// server records every request. A refused operation must leave the recorder
// empty: the gate runs before any network I/O.
func readOnlyClient(t *testing.T, opts ...Option) (*Client, *adtRecorder) {
	t.Helper()
	rec := &adtRecorder{}
	opts = append([]Option{WithReadOnly()}, opts...)
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, opts...)
	return client, rec
}

// requestsOtherThanProbes counts what reached the server, ignoring the CSRF
// token probe the transport sends ahead of a first write.
func requestsOtherThanProbes(rec *adtRecorder) []wireCall {
	var out []wireCall
	for _, c := range rec.snapshot() {
		if c.method == http.MethodHead || (c.method == http.MethodGet && strings.HasSuffix(c.path, "/core/discovery")) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func assertRefusedBeforeWire(t *testing.T, err error, rec *adtRecorder, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s must be refused under --read-only", what)
	}
	if calls := requestsOtherThanProbes(rec); len(calls) != 0 {
		t.Errorf("%s was refused but %d request(s) reached the server first", what, len(calls))
		dumpCalls(t, calls)
	}
}

// CreateTransport/ReleaseTransport (the v1 organizer calls) only looked at
// OpTransport, which --read-only does not cover.
func TestCreateTransport_RefusedUnderReadOnly(t *testing.T) {
	client, rec := readOnlyClient(t, WithEnableTransports())
	_, err := client.CreateTransport(t.Context(), "/sap/bc/adt/programs/programs/zdemo", "demo", "ZDEMO")
	assertRefusedBeforeWire(t, err, rec, "CreateTransport")
}

func TestReleaseTransport_RefusedUnderReadOnly(t *testing.T) {
	client, rec := readOnlyClient(t, WithEnableTransports())
	_, err := client.ReleaseTransport(t.Context(), "DEVK900001", false)
	assertRefusedBeforeWire(t, err, rec, "ReleaseTransport")
}

// The control: the same calls without --read-only do reach the server, so the
// two tests above are not passing because something else fails first.
func TestCreateTransport_ReachesServerWithoutReadOnly(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, WithEnableTransports())
	_, _ = client.CreateTransport(t.Context(), "/sap/bc/adt/programs/programs/zdemo", "demo", "ZDEMO")
	if len(requestsOtherThanProbes(rec)) == 0 {
		t.Fatal("without --read-only CreateTransport should send its request")
	}
}
