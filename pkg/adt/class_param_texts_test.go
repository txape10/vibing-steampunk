package adt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateClassParamTextParams(t *testing.T) {
	valid := SetClassParamTextParams{Class: "ZCL_X", Method: "M", Parameter: "IV_A", Description: "Factor"}

	tests := []struct {
		name    string
		mutate  func(*SetClassParamTextParams)
		wantErr string // "" = valid
	}{
		{"valid", func(p *SetClassParamTextParams) {}, ""},
		{"60 characters", func(p *SetClassParamTextParams) { p.Description = strings.Repeat("a", 60) }, ""},
		{"61 characters", func(p *SetClassParamTextParams) { p.Description = strings.Repeat("a", 61) }, "maximum is 60"},
		{"60 multibyte characters", func(p *SetClassParamTextParams) { p.Description = strings.Repeat("ñ", 60) }, ""},
		{"61 multibyte characters", func(p *SetClassParamTextParams) { p.Description = strings.Repeat("ñ", 61) }, "maximum is 60"},
		{"special characters", func(p *SetClassParamTextParams) { p.Description = `a < b & "c" > d` }, ""},
		{"tab", func(p *SetClassParamTextParams) { p.Description = "a\tb" }, "control characters"},
		{"line break", func(p *SetClassParamTextParams) { p.Description = "a\nb" }, "control characters"},
		{"emoji", func(p *SetClassParamTextParams) { p.Description = "ok 😀" }, "Basic Multilingual Plane"},
		{"surrounding blanks", func(p *SetClassParamTextParams) { p.Description = "  Factor  " }, ""},
		{"61 characters plus blanks", func(p *SetClassParamTextParams) { p.Description = " " + strings.Repeat("a", 61) }, "maximum is 60"},
		{"only blanks", func(p *SetClassParamTextParams) { p.Description = "   " }, "needs clear=true"},
		{"empty without clear", func(p *SetClassParamTextParams) { p.Description = "" }, "needs clear=true"},
		{"clear", func(p *SetClassParamTextParams) { p.Description = ""; p.Clear = true }, ""},
		{"transport", func(p *SetClassParamTextParams) { p.Transport = "A4HK900001" }, ""},
		{"transport lower case and blanks", func(p *SetClassParamTextParams) { p.Transport = " a4hk900001 " }, ""},
		{"transport is a task-like garbage", func(p *SetClassParamTextParams) { p.Transport = "A4HK90000" }, "not a request number"},
		{"transport with injection", func(p *SetClassParamTextParams) { p.Transport = "A4HK900001' OR 1=1" }, "not a request number"},
		{"clear with text", func(p *SetClassParamTextParams) { p.Clear = true }, "cannot be combined"},
		{"missing class", func(p *SetClassParamTextParams) { p.Class = " " }, "required"},
		{"missing method", func(p *SetClassParamTextParams) { p.Method = "" }, "required"},
		{"missing parameter", func(p *SetClassParamTextParams) { p.Parameter = "" }, "required"},
		{"object type interface", func(p *SetClassParamTextParams) { p.ObjectType = "intf" }, ""},
		{"object type class", func(p *SetClassParamTextParams) { p.ObjectType = "CLAS" }, ""},
		{"object type program", func(p *SetClassParamTextParams) { p.ObjectType = "PROG" }, "only supported for classes (CLAS) and interfaces (INTF)"},
		{"language empty", func(p *SetClassParamTextParams) { p.Language = "" }, ""},
		{"language SAP key", func(p *SetClassParamTextParams) { p.Language = "s" }, ""},
		{"language ISO", func(p *SetClassParamTextParams) { p.Language = "en" }, ""},
		{"language ISO unmapped", func(p *SetClassParamTextParams) { p.Language = "ET" }, "unknown language"},
		{"language too long", func(p *SetClassParamTextParams) { p.Language = "ESP" }, "unknown language"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid
			tt.mutate(&p)
			err := ValidateClassParamTextParams(p)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("expected valid, got %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected an error containing %q, got none", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("expected an error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestNormalizeParamTextObjectType(t *testing.T) {
	for in, want := range map[string]string{"": "", " ": "", "clas": "CLAS", "INTF": "INTF", " intf ": "INTF"} {
		got, err := NormalizeParamTextObjectType(in)
		if err != nil || got != want {
			t.Errorf("NormalizeParamTextObjectType(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeParamTextObjectType("FUGR"); err == nil {
		t.Error("FUGR must be refused")
	}
}

func TestParamTextPayloads_CarryObjectType(t *testing.T) {
	g := getParamTextsPayload("ZIF_X", "M", "EN", "INTF")
	if g["object_type"] != "INTF" || g["class_name"] != "ZIF_X" || g["method_name"] != "M" || g["language"] != "EN" {
		t.Errorf("get payload: %v", g)
	}
	if _, ok := getParamTextsPayload("ZCL_X", "", "", "")["object_type"]; ok {
		t.Error("an empty type must not be sent")
	}
	s := setParamTextPayload(SetClassParamTextParams{Class: "ZIF_X", ObjectType: "intf", Method: "M", Parameter: "IV_A", Description: " t "})
	if s["object_type"] != "INTF" || s["description"] != "t" {
		t.Errorf("set payload: %v", s)
	}
	if _, ok := setParamTextPayload(SetClassParamTextParams{Class: "ZCL_X"})["object_type"]; ok {
		t.Error("an empty type must not be sent")
	}
}

func TestWrapParamTextsError(t *testing.T) {
	err := wrapParamTextsError(errString("UNKNOWN_ACTION: Action 'setClassParamText' not supported"))
	if !strings.Contains(err.Error(), "restart Claude Desktop") {
		t.Errorf("an UNKNOWN_ACTION should explain the stale backend, got: %v", err)
	}
	other := errString("CLASS_NOT_FOUND: Class ZX not found")
	if got := wrapParamTextsError(other); got.Error() != other.Error() {
		t.Errorf("other errors must pass through unchanged, got: %v", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// The keys the ABAP side emits (pretty_mode low_case) must land in the Go
// types; a renamed key would otherwise only show up against a live system.
func TestClassParamTexts_DecodesABAPResponses(t *testing.T) {
	get := `{"class_name":"ZCL_X","object_type":"INTF","package_name":"$TMP","master_language":"S","language":"E",
	"method_list":[{"method_name":"M","cmp_type":"1","params":[
	{"name":"IV_A","kind":"parameter","decl_type":"0","version":"both","description":"Factor","has_text":true},
	{"name":"NO_EXISTE","kind":"exception","decl_type":"","version":"active","description":"","has_text":false}]}]}`
	var g ClassParamTexts
	if err := json.Unmarshal([]byte(get), &g); err != nil {
		t.Fatal(err)
	}
	if g.ObjectType != "INTF" || g.ClassName != "ZCL_X" || g.PackageName != "$TMP" || g.MasterLanguage != "S" || g.Language != "E" ||
		len(g.Methods) != 1 || g.Methods[0].MethodName != "M" || len(g.Methods[0].Params) != 2 ||
		g.Methods[0].Params[0].Name != "IV_A" || g.Methods[0].Params[0].Kind != "parameter" || g.Methods[0].Params[1].Kind != "exception" || g.Methods[0].Params[0].Version != "both" || !g.Methods[0].Params[0].HasText {
		t.Errorf("get response not decoded: %+v", g)
	}

	set := `{"status":"changed","class_name":"ZCL_X","object_type":"INTF","method_name":"M","parameter_name":"IV_A","kind":"exception","language":"E",
	"master_language":"S","old_description":"a","new_description":"b","row_state":"present","verified":true}`
	var s SetClassParamTextResult
	if err := json.Unmarshal([]byte(set), &s); err != nil {
		t.Fatal(err)
	}
	if s.ObjectType != "INTF" || s.Status != "changed" || s.ParameterName != "IV_A" || s.Kind != "exception" || s.OldDescription != "a" || s.NewDescription != "b" ||
		s.RowState != "present" || !s.Verified {
		t.Errorf("set response not decoded: %+v", s)
	}
}
