package scripting

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// The four Lua bindings that end in DebuggerSetVariableValue write into a
// running program. Under --read-only they must be refused — and refused
// first, not after a "checkpoint not found" or "no active recording" the
// binding would otherwise have answered with, which is why each script below
// uses an argument that cannot succeed.

func readOnlyLuaEngine(t *testing.T, opts ...adt.Option) (*LuaEngine, *bytes.Buffer, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("X-CSRF-Token", "TOKEN")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := adt.NewClient(srv.URL, "TESTUSER", "secret", opts...)
	engine := NewLuaEngine(client)
	t.Cleanup(engine.Close)
	var out bytes.Buffer
	engine.SetOutput(&out)
	return engine, &out, &hits
}

var variableWriteScripts = map[string]string{
	"setVariable":      `local ok, err = setVariable("LV_X", "1"); print(tostring(ok), err)`,
	"injectCheckpoint": `local ok, err = injectCheckpoint("no-such-checkpoint"); print(tostring(ok), err)`,
	"forceReplay":      `local ok, err = forceReplay("no-such-recording", -1, "%STORE%"); print(tostring(ok), err)`,
	"replayFromStep":   `local ok, err = replayFromStep(1); print(tostring(ok), err)`,
}

func TestLua_VariableWritesRefusedUnderReadOnly(t *testing.T) {
	for name, script := range variableWriteScripts {
		t.Run(name, func(t *testing.T) {
			engine, out, hits := readOnlyLuaEngine(t, adt.WithReadOnly())
			// forceReplay opens a recordings store; keep it out of the source tree
			script := strings.ReplaceAll(script, "%STORE%", filepath.ToSlash(t.TempDir()))
			if err := engine.Execute(script); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			got := out.String()
			if !strings.HasPrefix(got, "false") || !strings.Contains(got, "blocked by safety configuration") {
				t.Errorf("%s should answer false + the safety refusal, got %q", name, got)
			}
			if strings.Contains(got, "not found") || strings.Contains(got, "no active recording") {
				t.Errorf("%s answered with its own failure before the gate ran: %q", name, got)
			}
			if n := hits.Load(); n != 0 {
				t.Errorf("%s was refused but %d request(s) reached the server", name, n)
			}
		})
	}
}

// Control: without --read-only the same scripts get past the gate and fail for
// their own reasons (or reach the server), so the test above is not passing
// because those bindings fail anyway.
func TestLua_VariableWritesReachTheBindingWithoutReadOnly(t *testing.T) {
	for name, script := range variableWriteScripts {
		t.Run(name, func(t *testing.T) {
			engine, out, hits := readOnlyLuaEngine(t)
			// forceReplay opens a recordings store; keep it out of the source tree
			script := strings.ReplaceAll(script, "%STORE%", filepath.ToSlash(t.TempDir()))
			if err := engine.Execute(script); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			got := out.String()
			if strings.Contains(got, "blocked by safety configuration") {
				t.Fatalf("without --read-only %s must not be refused: %q", name, got)
			}
			if name == "setVariable" && hits.Load() == 0 {
				t.Errorf("setVariable without --read-only should send its request")
			}
		})
	}
}
