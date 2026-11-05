package adt

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

const renameOldURL = "/sap/bc/adt/programs/programs/zdemo_old"

func touchesOld(c wireCall) bool {
	return strings.EqualFold(c.path, renameOldURL) || strings.HasPrefix(strings.ToLower(c.path), renameOldURL+"/")
}

// A failed package check keeps the old object: no LOCK, no DELETE on it, and the
// new object is not rolled back either (vsp never deletes on its own).
func TestRenameObject_VerifyNewFails_KeepsBothObjects(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, renameHappyRoute(nil), WithAllowedPackages("$TMP"))

	verified := 0
	result, err := client.RenameObjectWithOptions(context.Background(), ObjectTypeProgram,
		"ZDEMO_OLD", "ZDEMO_NEW", "$TMP", "", &RenameObjectOptions{
			VerifyNew: func(ctx context.Context) (bool, string) {
				verified++
				return false, "MISMATCH: in ZABAP01."
			},
		})
	if err != nil {
		t.Fatalf("RenameObjectWithOptions: %v", err)
	}
	if verified != 1 {
		t.Fatalf("VerifyNew ran %d times, want 1", verified)
	}
	if result.Success || result.PackageCheck != "MISMATCH: in ZABAP01." {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.Contains(result.Message, "NOT deleted") || !strings.Contains(result.Message, "NOT rolled back") {
		t.Errorf("message should say nothing was deleted: %s", result.Message)
	}
	calls := rec.snapshot()
	for _, c := range calls {
		if c.method == http.MethodDelete {
			dumpCalls(t, calls)
			t.Fatalf("nothing may be deleted when the package check fails: %s", c)
		}
		if (isLock(c) || c.method == http.MethodDelete) && touchesOld(c) {
			dumpCalls(t, calls)
			t.Fatalf("the old object must not be locked or deleted: %s", c)
		}
	}
}

// With a passing check the order is: activate new < verify < lock old < delete old,
// and the check runs with no lock open.
func TestRenameObject_VerifyNewOK_RunsAfterActivationBeforeDelete(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, renameHappyRoute(nil), WithAllowedPackages("$TMP"))

	var atVerify []wireCall
	result, err := client.RenameObjectWithOptions(context.Background(), ObjectTypeProgram,
		"ZDEMO_OLD", "ZDEMO_NEW", "$TMP", "", &RenameObjectOptions{
			VerifyNew: func(ctx context.Context) (bool, string) {
				atVerify = rec.snapshot()
				return true, "ok: TADIR has the object in $TMP"
			},
		})
	if err != nil || !result.Success {
		t.Fatalf("rename should succeed: %v %+v", err, result)
	}
	if result.PackageCheck == "" {
		t.Error("PackageCheck should be reported on success too")
	}

	activateAt := indexOfCall(atVerify, func(c wireCall) bool {
		return c.method == http.MethodPost && strings.Contains(c.path, "/activation")
	})
	if activateAt < 0 {
		t.Fatalf("VerifyNew ran before the new object was activated: %v", atVerify)
	}
	locks, unlocks := 0, 0
	for _, c := range atVerify {
		switch {
		case isLock(c):
			locks++
		case isUnlock(c):
			unlocks++
		case c.method == http.MethodDelete:
			t.Errorf("old object already deleted when VerifyNew ran: %s", c)
		}
	}
	if locks != unlocks {
		t.Errorf("a lock was open while VerifyNew ran (%d LOCK, %d UNLOCK): a stateless read there would end its window (#91)", locks, unlocks)
	}
	if indexOfCall(rec.snapshot(), func(c wireCall) bool { return c.method == http.MethodDelete }) < 0 {
		t.Error("old object should be deleted after a passing check")
	}
}

// IncompleteNote is reported when the create itself fails.
func TestRenameObject_IncompleteNoteOnCreateFailure(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, renameHappyRoute(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/programs/programs") {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		return false
	}), WithAllowedPackages("$TMP"))

	result, err := client.RenameObjectWithOptions(context.Background(), ObjectTypeProgram,
		"ZDEMO_OLD", "ZDEMO_NEW", "$TMP", "", &RenameObjectOptions{IncompleteNote: " NOTE: may exist in ZABAP01"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e, "may exist in ZABAP01") {
			found = true
		}
	}
	if !found || result.Success {
		t.Errorf("IncompleteNote missing: %+v", result)
	}
}

// CreateFromFile marks ObjectCreated on a failure AFTER the object was created,
// and not on a failure of the create itself.
func TestCreateFromFile_ObjectCreatedOnlyAfterCreate(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/zdemo_new.prog.abap"
	if err := os.WriteFile(file, []byte("REPORT zdemo_new.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create fails -> not created.
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, renameHappyRoute(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/programs/programs") {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		return false
	}), WithAllowedPackages("$TMP"))
	res, err := client.CreateFromFile(context.Background(), file, "$TMP", "")
	if err != nil || res == nil || res.Success || res.ObjectCreated {
		t.Fatalf("a failed create must not be marked created: %v %+v", err, res)
	}

	// Create works, the lock fails -> created, but not a success.
	rec = &adtRecorder{}
	client = newStubbedClient(t, rec, renameHappyRoute(func(w http.ResponseWriter, r *http.Request) bool {
		if isLock(wireCall{method: r.Method, query: r.URL.Query()}) {
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		return false
	}), WithAllowedPackages("$TMP"))
	res, err = client.CreateFromFile(context.Background(), file, "$TMP", "")
	if err != nil || res == nil || res.Success || !res.ObjectCreated {
		t.Fatalf("a failure after the create must be marked created: %v %+v", err, res)
	}
}
