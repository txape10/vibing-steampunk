package saprfc

import (
	"testing"
)

func TestSelectionRows_Defaults(t *testing.T) {
	rows := selectionRows([]ReportParam{{Name: "p_matnr", Low: "4711"}})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	want := map[string]any{
		"SELNAME": "P_MATNR", "KIND": "P", "SIGN": "I", "OPTION": "EQ",
		"LOW": "4711", "HIGH": "",
	}
	for k, v := range want {
		if r[k] != v {
			t.Errorf("row[%q] = %v, want %v", k, r[k], v)
		}
	}
}

func TestSelectionRows_ExplicitNotOverridden(t *testing.T) {
	rows := selectionRows([]ReportParam{{
		Name: "s_werks", Kind: "S", Sign: "I", Option: "BT", Low: "1000", High: "2000",
	}})
	r := rows[0]
	want := map[string]any{
		"SELNAME": "S_WERKS", "KIND": "S", "SIGN": "I", "OPTION": "BT",
		"LOW": "1000", "HIGH": "2000",
	}
	for k, v := range want {
		if r[k] != v {
			t.Errorf("row[%q] = %v, want %v", k, r[k], v)
		}
	}
}

func TestSelectionRows_Empty(t *testing.T) {
	if rows := selectionRows(nil); len(rows) != 0 {
		t.Errorf("selectionRows(nil) = %v, want empty", rows)
	}
}

func TestBapiError_TypeE(t *testing.T) {
	ret := map[string]any{"TYPE": "E", "MESSAGE": "job could not be opened", "ID": "BT", "NUMBER": "123"}
	err := bapiError("BAPI_XBP_JOB_OPEN", ret)
	if err == nil {
		t.Fatal("expected an error for TYPE=E")
	}
	if got := err.Error(); got != "BAPI_XBP_JOB_OPEN: job could not be opened" {
		t.Errorf("unexpected message: %q", got)
	}
}

func TestBapiError_TypeA(t *testing.T) {
	ret := map[string]any{"TYPE": "A", "MESSAGE": "termination"}
	if err := bapiError("X", ret); err == nil {
		t.Fatal("expected an error for TYPE=A (abort)")
	}
}

func TestBapiError_FallsBackToIDNumber(t *testing.T) {
	// A real BAPIRET2 always carries every field (zero-valued, never absent) —
	// MESSAGE="" here mirrors that, unlike an omitted key (which fmt.Sprint's as
	// the literal string "<nil>" and would never hit the fallback below).
	ret := map[string]any{"TYPE": "E", "MESSAGE": "", "ID": "BT", "NUMBER": "042"}
	err := bapiError("X", ret)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); got != "X: BT042" {
		t.Errorf("unexpected message: %q", got)
	}
}

func TestBapiError_Success(t *testing.T) {
	cases := []any{
		map[string]any{"TYPE": "S", "MESSAGE": "ok"},
		map[string]any{"TYPE": ""},
		nil,
		"not a map",
	}
	for _, ret := range cases {
		if err := bapiError("X", ret); err != nil {
			t.Errorf("bapiError(%v) = %v, want nil", ret, err)
		}
	}
}
