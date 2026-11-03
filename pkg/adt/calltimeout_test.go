package adt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func slowServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "T")
		time.Sleep(delay)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newSlowTransport(ts *httptest.Server, requestTimeout time.Duration) *Transport {
	cfg := NewConfig(ts.URL, "u", "p")
	cfg.Timeout = requestTimeout
	return NewTransport(cfg)
}

func getWith(t *testing.T, tr *Transport, ctx context.Context, _ bool) error {
	t.Helper()
	opts := &RequestOptions{Method: http.MethodGet}
	resp, err := tr.Request(ctx, "/sap/bc/adt/x", opts)
	if resp != nil && err == nil && resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	return err
}

// Without a call deadline the client's per-request Timeout ends a slow request.
func TestCallDeadline_WithoutMarkPerRequestTimeoutApplies(t *testing.T) {
	ts := slowServer(t, 400*time.Millisecond)
	tr := newSlowTransport(ts, 100*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := getWith(t, tr, ctx, true); err == nil {
		t.Fatal("expected the per-request timeout to cut the request off")
	}
}

// With the mark, the call's own deadline governs and the request completes.
func TestCallDeadline_MarkLiftsPerRequestTimeout(t *testing.T) {
	ts := slowServer(t, 400*time.Millisecond)
	tr := newSlowTransport(ts, 100*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := getWith(t, tr, WithCallDeadline(ctx), true); err != nil {
		t.Fatalf("request under a call deadline failed: %v", err)
	}
}

// The deadline itself still ends the request.
func TestCallDeadline_DeadlineStillEndsRequest(t *testing.T) {
	ts := slowServer(t, 600*time.Millisecond)
	tr := newSlowTransport(ts, 100*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := getWith(t, tr, WithCallDeadline(ctx), true); err == nil {
		t.Fatal("expected the call deadline to end the request")
	}
}

// A context without a deadline is returned unmarked, so nothing is unbounded.
func TestWithCallDeadline_NoDeadlineStaysUnmarked(t *testing.T) {
	ctx := WithCallDeadline(context.Background())
	if callDeadlineGoverns(ctx) {
		t.Fatal("a context with no deadline must not govern")
	}
	// A context detached from its deadline falls back to the client timeout.
	dctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	marked := WithCallDeadline(dctx)
	if !callDeadlineGoverns(marked) {
		t.Fatal("a marked context with a deadline must govern")
	}
	if callDeadlineGoverns(context.WithoutCancel(marked)) {
		t.Fatal("a detached context must fall back to the per-request timeout")
	}
}

// A slow stateless request under a call deadline must not hold the context
// gate: a stateful request (a LOCK) proceeds meanwhile.
func TestCallDeadline_StatelessRunDoesNotBlockStateful(t *testing.T) {
	var slow atomic.Bool
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "T")
		if strings.HasSuffix(r.URL.Path, "/slow") {
			slow.Store(true)
			<-release
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer ts.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	tr := newSlowTransport(ts, 30*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := tr.Request(WithCallDeadline(ctx), "/sap/bc/adt/slow", &RequestOptions{Method: http.MethodGet})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !slow.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !slow.Load() {
		t.Fatal("slow request never reached the server")
	}

	stateful := make(chan error, 1)
	go func() {
		_, err := tr.Request(context.Background(), "/sap/bc/adt/fast", &RequestOptions{Method: http.MethodGet, Stateful: true})
		stateful <- err
	}()
	select {
	case err := <-stateful:
		if err != nil {
			t.Fatalf("stateful request failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a long stateless run under a call deadline blocked a stateful request")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("slow request failed: %v", err)
	}
}
