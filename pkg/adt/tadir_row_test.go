package adt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tadirStub(t *testing.T, rows [][]string, status int, bodies *[]string) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if strings.HasSuffix(r.URL.Path, "/datapreview/freestyle") {
			raw, _ := io.ReadAll(r.Body)
			*bodies = append(*bodies, string(raw))
			if status != 0 {
				w.WriteHeader(status)
				return
			}
			_, _ = io.WriteString(w, dpXML([]string{"DEVCLASS", "DELFLAG"}, rows))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return NewClient(ts.URL, "u", "p")
}

func TestTADIRRow_StaleDeletedRow(t *testing.T) {
	var bodies []string
	c := tadirStub(t, [][]string{{"ZABAP01", "X"}}, 0, &bodies)
	got, err := c.TADIRRow(context.Background(), "PROG", "zvsp_tst_x")
	if err != nil || !got.Found || !got.Deleted || got.Package != "ZABAP01" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], "obj_name = 'ZVSP_TST_X'") || !strings.Contains(bodies[0], "object = 'PROG'") {
		t.Errorf("query not as expected: %v", bodies)
	}
}

func TestTADIRRow_LiveRowAndMissingRow(t *testing.T) {
	var bodies []string
	c := tadirStub(t, [][]string{{"ZSU01", ""}}, 0, &bodies)
	got, err := c.TADIRRow(context.Background(), "CLAS/OC", "ZCL_X")
	if err != nil || !got.Found || got.Deleted || got.Package != "ZSU01" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if !strings.Contains(bodies[0], "object = 'CLAS'") {
		t.Errorf("type not reduced to its TADIR root: %v", bodies)
	}
	c = tadirStub(t, nil, 0, &bodies)
	if got, err := c.TADIRRow(context.Background(), "PROG", "ZNONE"); err != nil || got.Found {
		t.Errorf("a missing row is not an error and not found: %+v %v", got, err)
	}
}

func TestTADIRRow_RejectsInjectionWithoutSendingAnything(t *testing.T) {
	var bodies []string
	c := tadirStub(t, nil, 0, &bodies)
	for _, name := range []string{"ZX' OR 1=1 --", "A B", ""} {
		if _, err := c.TADIRRow(context.Background(), "PROG", name); err == nil {
			t.Errorf("name %q must be refused", name)
		}
	}
	if len(bodies) != 0 {
		t.Errorf("a refused lookup must send nothing, sent %v", bodies)
	}
}

func TestTADIRRow_ReadFailureIsAnError(t *testing.T) {
	var bodies []string
	c := tadirStub(t, nil, http.StatusInternalServerError, &bodies)
	if _, err := c.TADIRRow(context.Background(), "PROG", "ZX"); err == nil {
		t.Error("a failed read must be an error, not 'not found'")
	}
}
