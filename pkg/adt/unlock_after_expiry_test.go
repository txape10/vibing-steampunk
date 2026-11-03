package adt

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// A call that spends its whole budget on the write must still release the
// lock: the plain UNLOCK on the expired context never leaves the process.
func TestWithObjectLock_UnlockSentWhenContextExpiresDuringWrite(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/programs/ZDEMO_SCOPE"
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, lockScopeRoute, WithAllowedPackages("$TMP"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := client.WithObjectLock(ctx, "UpdateSource", objectURL, "",
		func(ctx context.Context, handle, transport string) error {
			if err := client.UpdateSource(ctx, objectURL+"/source/main", "REPORT zdemo_scope.\n", handle, transport); err != nil {
				return err
			}
			cancel() // the budget ran out right after the write
			return nil
		})
	if err != nil {
		t.Fatalf("WithObjectLock: %v", err)
	}
	if indexOfCall(rec.snapshot(), isUnlock) < 0 {
		dumpCalls(t, rec.snapshot())
		t.Fatal("no UNLOCK went out after the context expired")
	}
	if client.lockOutstanding() {
		t.Error("lock window still open after the release")
	}
}

// When even the retried UNLOCK fails, the original error is reported.
func TestUnlockAfterWrite_ReportsFailureWhenRetryFails(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := client.unlockAfterWrite(ctx, "/sap/bc/adt/programs/programs/zx", "H")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want the original context error, got %v", err)
	}
	n := 0
	for _, c := range rec.snapshot() {
		if isUnlock(c) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly the detached retry on the wire, got %d UNLOCKs", n)
	}
}

// A live context never triggers the detached retry.
func TestUnlockAfterWrite_NoRetryOnLiveContext(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if err := client.unlockAfterWrite(context.Background(), "/sap/bc/adt/programs/programs/zx", "H"); err == nil {
		t.Fatal("expected the failure to be returned")
	}
	n := 0
	for _, c := range rec.snapshot() {
		if isUnlock(c) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one UNLOCK on a live context, got %d", n)
	}
}
