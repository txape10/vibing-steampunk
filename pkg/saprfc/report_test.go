package saprfc

import (
	"strings"
	"testing"
)

func TestJobStatusText(t *testing.T) {
	cases := map[string]string{
		"P":   "scheduled",
		"S":   "released",
		"R":   "running",
		"F":   "finished",
		"A":   "cancelled",
		"Y":   "ready",
		"":    "",
		"z":   "unknown",
		"XX":  "unknown",
		" p ": "scheduled", // TBTCO can pad the field
	}
	for in, want := range cases {
		if got := jobStatusText(in); got != want {
			t.Errorf("jobStatusText(%q) = %q, want %q", in, got, want)
		}
	}
}

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

func TestValidJobName_RejectsInjection(t *testing.T) {
	cases := []string{
		"VSP' OR JOBNAME LIKE 'Z",
		"X'; DELETE FROM TBTCO WHERE ''='",
		"",
		strings.Repeat("A", 33), // longer than TBTCO-JOBNAME (CHAR 32)
	}
	for _, name := range cases {
		if validJobName(name) {
			t.Errorf("validJobName(%q) = true, want false", name)
		}
	}
}

func TestValidJobName_AcceptsRealNames(t *testing.T) {
	cases := []string{"VSP_ZTESTRCG1", "job-name_123", "A"}
	for _, name := range cases {
		if !validJobName(name) {
			t.Errorf("validJobName(%q) = false, want true", name)
		}
	}
}

func TestValidJobCount_RejectsInjection(t *testing.T) {
	cases := []string{"12345678' OR '1'='1", "", "123456789", "abcdefgh"}
	for _, count := range cases {
		if validJobCount(count) {
			t.Errorf("validJobCount(%q) = true, want false", count)
		}
	}
}

func TestValidJobCount_AcceptsRealCounts(t *testing.T) {
	cases := []string{"00000001", "12345678", "1"}
	for _, count := range cases {
		if !validJobCount(count) {
			t.Errorf("validJobCount(%q) = false, want true", count)
		}
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
