package mcp

import (
	"strings"
	"testing"
)

func setParamTextArgs(params map[string]any) map[string]any {
	return map[string]any{"action": "edit", "target": "CLAS ZCL_DEMO", "params": params}
}

func validSetParams() map[string]any {
	return map[string]any{"type": "set_param_text", "method": "M", "parameter": "IV_A", "description": "Factor"}
}

func TestParamTexts_SetRefusedUnderReadOnly(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, readOnly)
	before := rc.count()
	out := callSAP(t, server, setParamTextArgs(validSetParams()))
	assertBlocked(t, out)
	if n := rc.count() - before; n != 0 {
		t.Errorf("a refused SetClassParamText sent %d request(s)", n)
	}
}

// Control: without --read-only the same call gets past the gates and reaches
// the ADT host (the package lookup, then the WebSocket upgrade).
func TestParamTexts_SetReachesHostWithoutReadOnly(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, nil)
	before := rc.count()
	out := callSAP(t, server, setParamTextArgs(validSetParams()))
	if strings.Contains(out, "blocked by safety configuration") {
		t.Fatalf("without --read-only the call must not be refused: %s", out)
	}
	if rc.count() == before {
		t.Error("without --read-only the call should reach the ADT host")
	}
}

func TestParamTexts_GetStillConnectsUnderReadOnly(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, readOnly)
	before := rc.count()
	out := callSAP(t, server, map[string]any{"action": "read", "target": "CLAS ZCL_DEMO",
		"params": map[string]any{"type": "param_texts"}})
	if strings.Contains(out, "blocked by safety configuration") {
		t.Fatalf("reading parameter texts must not be refused under --read-only: %s", out)
	}
	if rc.count() == before {
		t.Error("reading parameter texts should reach the ADT host under --read-only")
	}
	// Only the new route names its tool in the failure (a plain read of the
	// class would not), which proves the request went through it.
	if !strings.Contains(out, "GetClassParamTexts") {
		t.Errorf("expected the answer to come from the GetClassParamTexts handler, got: %s", out)
	}
}

// Everything decidable locally is refused before any request leaves.
func TestParamTexts_SetLocalRefusalsSendNothing(t *testing.T) {
	cases := map[string]struct {
		target string
		mutate func(map[string]any)
		want   string
	}{
		"61 characters":        {"CLAS ZCL_DEMO", func(p map[string]any) { p["description"] = strings.Repeat("a", 61) }, "maximum is 60"},
		"empty without clear":  {"CLAS ZCL_DEMO", func(p map[string]any) { p["description"] = "" }, "needs clear=true"},
		"clear with text":      {"CLAS ZCL_DEMO", func(p map[string]any) { p["clear"] = true }, "cannot be combined"},
		"unknown ISO language": {"CLAS ZCL_DEMO", func(p map[string]any) { p["language"] = "ET" }, "unknown language"},
		"program":              {"PROG ZDEMO", func(p map[string]any) {}, "only supported for classes (CLAS) and interfaces (INTF)"},
		"object_type override": {"INTF ZIF_DEMO", func(p map[string]any) { p["object_type"] = "PROG" }, "only supported for classes (CLAS) and interfaces (INTF)"},
		"transport format":     {"CLAS ZCL_DEMO", func(p map[string]any) { p["transport"] = "NOT-A-REQUEST" }, "not a request number"},
		"missing parameter":    {"CLAS ZCL_DEMO", func(p map[string]any) { delete(p, "parameter") }, "class, method and parameter are required"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			server, rc, _ := newReadOnlyTestServer(t, nil)
			params := validSetParams()
			c.mutate(params)
			before := rc.count()
			out := callSAP(t, server, map[string]any{"action": "edit", "target": c.target, "params": params})
			if !strings.Contains(out, c.want) {
				t.Errorf("expected %q in the refusal, got: %s", c.want, out)
			}
			if n := rc.count() - before; n != 0 {
				t.Errorf("a locally refused call sent %d request(s)", n)
			}
		})
	}
}

// A request number is a policy question before it is a SAP one: without
// --allow-transportable-edits the call is refused and nothing is sent.
func TestParamTexts_SetWithTransportBlockedWithoutTransportableEdits(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, nil)
	params := validSetParams()
	params["transport"] = "A4HK000001"
	before := rc.count()
	out := callSAP(t, server, setParamTextArgs(params))
	if !strings.Contains(out, "editing transportable objects is disabled") {
		t.Errorf("expected the transportable-edits refusal, got: %s", out)
	}
	if n := rc.count() - before; n != 0 {
		t.Errorf("a refused call sent %d request(s)", n)
	}
}

func TestParamTexts_SetWithTransportOutsideWhitelistIsBlocked(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, func(c *Config) {
		c.AllowTransportableEdits = true
		c.AllowedTransports = []string{"A4HK9*"}
	})
	params := validSetParams()
	params["transport"] = "A4HK000001"
	before := rc.count()
	out := callSAP(t, server, setParamTextArgs(params))
	assertBlocked(t, out)
	if n := rc.count() - before; n != 0 {
		t.Errorf("a refused call sent %d request(s)", n)
	}
}

// Control for the two refusals above: with the flag and a matching whitelist
// the same call gets past the policy and reaches the ADT host.
func TestParamTexts_SetWithAllowedTransportReachesHost(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, func(c *Config) {
		c.AllowTransportableEdits = true
		c.AllowedTransports = []string{"A4HK0*"}
	})
	params := validSetParams()
	params["transport"] = "a4hk000001"
	before := rc.count()
	out := callSAP(t, server, setParamTextArgs(params))
	if strings.Contains(out, "blocked by safety configuration") || strings.Contains(out, "transportable objects is disabled") {
		t.Fatalf("an allowed transport must not be refused by policy: %s", out)
	}
	if rc.count() == before {
		t.Error("an allowed transport should reach the ADT host")
	}
}

// An interface goes through the same gates as a class: refused under
// --read-only before anything is sent, and, with a control, past them without it.
func TestParamTexts_SetInterfaceRefusedUnderReadOnly(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, readOnly)
	before := rc.count()
	out := callSAP(t, server, map[string]any{"action": "edit", "target": "INTF ZIF_DEMO", "params": validSetParams()})
	assertBlocked(t, out)
	if n := rc.count() - before; n != 0 {
		t.Errorf("a refused SetClassParamText on an interface sent %d request(s)", n)
	}
}

func TestParamTexts_SetInterfaceReachesHostWithoutReadOnly(t *testing.T) {
	server, rc, _ := newReadOnlyTestServer(t, nil)
	before := rc.count()
	out := callSAP(t, server, map[string]any{"action": "edit", "target": "INTF ZIF_DEMO", "params": validSetParams()})
	if strings.Contains(out, "blocked by safety configuration") || strings.Contains(out, "only supported for classes") {
		t.Fatalf("an interface must not be refused: %s", out)
	}
	if rc.count() == before {
		t.Error("an interface should reach the ADT host")
	}
}

func TestParamTexts_GetInterfaceUsesTheHandler(t *testing.T) {
	server, _, _ := newReadOnlyTestServer(t, readOnly)
	out := callSAP(t, server, map[string]any{"action": "read", "target": "INTF ZIF_DEMO",
		"params": map[string]any{"type": "param_texts"}})
	if strings.Contains(out, "only supported for classes") {
		t.Fatalf("reading an interface must not be refused: %s", out)
	}
	if !strings.Contains(out, "GetClassParamTexts: WebSocket connect failed") {
		t.Errorf("expected the answer to come from the GetClassParamTexts handler, got: %s", out)
	}
}

// The package and transport policies must look at the object that is written:
// an interface lives under /oo/interfaces/, not /oo/classes/.
func TestParamTextObjectURL(t *testing.T) {
	cases := map[string]string{
		"":     "/sap/bc/adt/oo/classes/zcl_demo",
		"CLAS": "/sap/bc/adt/oo/classes/zcl_demo",
		"INTF": "/sap/bc/adt/oo/interfaces/zcl_demo",
	}
	for objectType, want := range cases {
		if got := paramTextObjectURL(objectType, "ZCL_DEMO"); got != want {
			t.Errorf("paramTextObjectURL(%q) = %q, want %q", objectType, got, want)
		}
	}
}

func TestParamTextArgs(t *testing.T) {
	args := paramTextArgs("CLAS", "ZCL_DEMO", map[string]any{
		"method": "M", "parameter": "IV_A", "clear": true, "language": "EN",
	})
	if args["class"] != "ZCL_DEMO" || args["object_type"] != "CLAS" {
		t.Errorf("target not split into class/object_type: %v", args)
	}
	if v, ok := args["clear"].(bool); !ok || !v {
		t.Errorf("clear must reach the handler as a bool, got %v", args["clear"])
	}
	override := paramTextArgs("CLAS", "ZCL_DEMO", map[string]any{"class": "ZCL_OTHER"})
	if override["class"] != "ZCL_OTHER" {
		t.Errorf("params.class should override the target, got %v", override["class"])
	}
	typed := paramTextArgs("CLAS", "ZIF_DEMO", map[string]any{"object_type": "INTF"})
	if typed["object_type"] != "INTF" {
		t.Errorf("params.object_type should override the target type, got %v", typed["object_type"])
	}
}
