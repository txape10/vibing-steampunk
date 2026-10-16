package adt

import (
	"context"
	"net"
	"net/http"
	"testing"
)

func packageExistsAgainst(t *testing.T, status int, body string) bool {
	t.Helper()
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return client.packageExists(context.Background(), "ZDEMO_PKG")
}

func TestPackageExists_OnlySAPsAnswerMeansMissing(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"404 from SAP is missing", http.StatusNotFound, "No suitable resource found", false},
		{"not found in SAP's message is missing", http.StatusInternalServerError, "Package ZDEMO_PKG not found", false},
		{"another failure stays optimistic", http.StatusInternalServerError, "boom", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := packageExistsAgainst(t, tc.status, tc.body); got != tc.want {
				t.Errorf("packageExists = %v, want %v", got, tc.want)
			}
		})
	}
}

// A transport failure whose text carries "404" (a port such as :40457) is not
// SAP saying the package is missing.
func TestPackageExists_TransportErrorWithFourOhFourInURLIsNotMissing(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing listens here any more: the dial fails
	cfg := NewConfig("http://"+addr+"/x404", "TESTUSER", "secret")
	client := NewClientWithTransport(cfg, NewTransport(cfg))
	if !client.packageExists(context.Background(), "ZDEMO_PKG") {
		t.Error("a connection failure was read as a missing package")
	}
}
