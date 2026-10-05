package adt

import (
	"net/http"
	"strings"
	"testing"
)

// Every gate below runs before the request is built, so a refused call must
// leave the recording server empty — and the control cases prove the same call
// does reach the server once the gate lets it through.

func TestLockObject_RefusedUnderReadOnly(t *testing.T) {
	for _, mode := range []string{"MODIFY", "", "modify", " Modify ", "EXCLUSIVE", "DELETE"} {
		t.Run("mode="+mode, func(t *testing.T) {
			client, rec := readOnlyClient(t)
			_, err := client.LockObject(t.Context(), "/sap/bc/adt/programs/programs/zdemo", mode)
			assertRefusedBeforeWire(t, err, rec, "LockObject("+mode+")")
		})
	}
}

func TestLockObject_ReadModeStillAllowedUnderReadOnly(t *testing.T) {
	client, rec := readOnlyClient(t)
	_, _ = client.LockObject(t.Context(), "/sap/bc/adt/programs/programs/zdemo", "read")
	if len(requestsOtherThanProbes(rec)) == 0 {
		t.Fatal("a READ lock is not a write and must reach the server under --read-only")
	}
}

func TestLockObject_ModifyReachesServerWithoutReadOnly(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	_, _ = client.LockObject(t.Context(), "/sap/bc/adt/programs/programs/zdemo", "MODIFY")
	if len(requestsOtherThanProbes(rec)) == 0 {
		t.Fatal("without --read-only the MODIFY lock should be sent")
	}
}

func TestSetPrettyPrinterSettings_RefusedUnderReadOnly(t *testing.T) {
	client, rec := readOnlyClient(t)
	err := client.SetPrettyPrinterSettings(t.Context(), &PrettyPrinterSettings{Indentation: true, Style: "keywordUpper"})
	assertRefusedBeforeWire(t, err, rec, "SetPrettyPrinterSettings")
}

func TestServiceBindingPublish_RefusedUnderReadOnly(t *testing.T) {
	client, rec := readOnlyClient(t)
	_, err := client.PublishServiceBinding(t.Context(), "ZDEMO_SB", "0001")
	assertRefusedBeforeWire(t, err, rec, "PublishServiceBinding")

	client, rec = readOnlyClient(t)
	_, err = client.UnpublishServiceBinding(t.Context(), "ZDEMO_SB", "0001")
	assertRefusedBeforeWire(t, err, rec, "UnpublishServiceBinding")
}

func TestDebuggerSetVariableValue_RefusedUnderReadOnly(t *testing.T) {
	client, rec := readOnlyClient(t)
	_, err := client.DebuggerSetVariableValue(t.Context(), "LV_X", "1")
	assertRefusedBeforeWire(t, err, rec, "DebuggerSetVariableValue")
}

// Control for the refusals above that only a gate can produce: with --read-only
// off, each call goes out, so none of them is "refused" by failing early.
func TestGatedCalls_ReachTheServerWithoutReadOnly(t *testing.T) {
	ctx := t.Context()
	calls := map[string]func(*Client){
		"SetPrettyPrinterSettings": func(c *Client) {
			_ = c.SetPrettyPrinterSettings(ctx, &PrettyPrinterSettings{Indentation: true, Style: "keywordUpper"})
		},
		"PublishServiceBinding":    func(c *Client) { _, _ = c.PublishServiceBinding(ctx, "ZDEMO_SB", "0001") },
		"UnpublishServiceBinding":  func(c *Client) { _, _ = c.UnpublishServiceBinding(ctx, "ZDEMO_SB", "0001") },
		"DebuggerSetVariableValue": func(c *Client) { _, _ = c.DebuggerSetVariableValue(ctx, "LV_X", "1") },
		"ReleaseTransport":         func(c *Client) { _, _ = c.ReleaseTransport(ctx, "DEVK900001", false) },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			rec := &adtRecorder{}
			client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}, WithEnableTransports())
			call(client)
			if len(requestsOtherThanProbes(rec)) == 0 {
				t.Errorf("without --read-only %s should send its request", name)
			}
		})
	}
}

func TestUnitTests_DangerousOrCriticalRefusedUnderReadOnly(t *testing.T) {
	const url = "/sap/bc/adt/oo/classes/zcl_demo"
	for name, flags := range map[string]UnitTestRunFlags{
		"dangerous": {Harmless: true, Dangerous: true},
		"critical":  {Harmless: true, Critical: true},
	} {
		t.Run(name, func(t *testing.T) {
			f := flags
			client, rec := readOnlyClient(t)
			_, err := client.RunUnitTests(t.Context(), url, &f)
			assertRefusedBeforeWire(t, err, rec, "RunUnitTests("+name+")")

			client, rec = readOnlyClient(t)
			_, err = client.GetCodeCoverage(t.Context(), url, &f)
			assertRefusedBeforeWire(t, err, rec, "GetCodeCoverage("+name+")")
		})
	}
}

// An ordinary (harmless-only) run is a read and keeps working under --read-only,
// including when the caller passes no flags at all.
func TestUnitTests_HarmlessRunStillAllowedUnderReadOnly(t *testing.T) {
	const url = "/sap/bc/adt/oo/classes/zcl_demo"
	harmless := DefaultUnitTestFlags()
	for name, flags := range map[string]*UnitTestRunFlags{"default flags": &harmless, "nil flags": nil} {
		t.Run(name, func(t *testing.T) {
			client, rec := readOnlyClient(t)
			_, _ = client.RunUnitTests(t.Context(), url, flags)
			if len(requestsOtherThanProbes(rec)) == 0 {
				t.Fatal("a harmless unit-test run must still reach the server under --read-only")
			}
		})
	}
}

func TestGctsWrites_RefusedUnderReadOnly(t *testing.T) {
	ctx := t.Context()
	writes := map[string]func(*Client) error{
		"create": func(c *Client) error { _, e := c.GctsCreateRepository(ctx, GctsCreateOptions{}); return e },
		"delete": func(c *Client) error { return c.GctsDeleteRepository(ctx, "R1") },
		"clone":  func(c *Client) error { return c.GctsCloneRepository(ctx, "R1") },
		"pull":   func(c *Client) error { _, e := c.GctsPull(ctx, "R1", ""); return e },
		"commit": func(c *Client) error { _, e := c.GctsCommit(ctx, "R1", GctsCommitOptions{}); return e },
		"switch": func(c *Client) error { return c.GctsSwitchBranch(ctx, "R1", "main") },
	}
	for name, call := range writes {
		t.Run(name, func(t *testing.T) {
			client, rec := readOnlyClient(t, WithEnableTransports())
			assertRefusedBeforeWire(t, call(client), rec, "gCTS "+name)
		})
	}

	// --transport-read-only now covers them as well
	client, rec := readOnlyClient(t, WithEnableTransports(), WithTransportReadOnly())
	assertRefusedBeforeWire(t, client.GctsCloneRepository(ctx, "R1"), rec, "gCTS clone under --transport-read-only")
}

func TestGctsReads_StillAllowedUnderReadOnly(t *testing.T) {
	client, rec := readOnlyClient(t, WithEnableTransports())
	_, _ = client.GctsListRepositories(t.Context())
	if len(requestsOtherThanProbes(rec)) == 0 {
		t.Fatal("listing gCTS repositories is a read and must reach the server under --read-only")
	}
}

// The error text names the mode, so a user knows which switch refused the call.
func TestReadOnlyRefusals_NameTheCause(t *testing.T) {
	client, _ := readOnlyClient(t)
	_, err := client.LockObject(t.Context(), "/sap/bc/adt/programs/programs/zdemo", "MODIFY")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("LockObject error should mention read-only mode, got: %v", err)
	}
}
