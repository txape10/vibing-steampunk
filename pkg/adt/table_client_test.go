package adt

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestGenerateTableDDL_ClientField(t *testing.T) {
	plain := []TableField{{Name: "ID", Type: "CHAR10", IsKey: true}, {Name: "TXT", Type: "STRING"}}
	own := []TableField{{Name: "MANDT", Type: "MANDT", IsKey: true}, {Name: "ID", Type: "CHAR10", IsKey: true}}

	tests := []struct {
		name       string
		opts       CreateTableOptions
		wantClient int // number of "abap.clnt" / mandt key lines expected
		wantErr    string
	}{
		{"no client field: one is added", CreateTableOptions{Name: "ZT", Fields: plain}, 1, ""},
		{"own MANDT first: nothing added", CreateTableOptions{Name: "ZT", Fields: own}, 1, ""},
		{"client independent: nothing added", CreateTableOptions{Name: "ZT", Fields: plain, ClientDependent: boolPtr(false)}, 0, ""},
		{"own MANDT contradicts client_dependent false", CreateTableOptions{Name: "ZT", Fields: own, ClientDependent: boolPtr(false)}, 0, "client_dependent is false"},
		{"MANDT later in the key", CreateTableOptions{Name: "ZT", Fields: []TableField{{Name: "ID", Type: "CHAR10", IsKey: true}, {Name: "MANDT", Type: "MANDT", IsKey: true}}}, 0, "only the first key field"},
		{"field named CLIENT collides", CreateTableOptions{Name: "ZT", Fields: []TableField{{Name: "CLIENT", Type: "CHAR3", IsKey: true}, {Name: "ID", Type: "CHAR10", IsKey: true}}}, 0, "built-in type"},
		{"builtin MANDT-named first key refused", CreateTableOptions{Name: "ZT", Fields: []TableField{{Name: "MANDT", Type: "CHAR3", IsKey: true}}}, 0, "never treats as a client field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.opts.TableCategory, tt.opts.DeliveryClass = "TRANSPARENT", "A"
			ddl, err := generateTableDDL(tt.opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("generateTableDDL: %v", err)
			}
			got := strings.Count(ddl, "abap.clnt") + strings.Count(ddl, ": mandt")
			if got != tt.wantClient {
				t.Errorf("client key fields = %d, want %d\n%s", got, tt.wantClient, ddl)
			}
		})
	}
}

// An invalid specification must be refused before anything leaves the process:
// no transport check, no object.
func TestCreateTable_InvalidClientSpecSendsNothing(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	err := client.CreateTable(context.Background(), CreateTableOptions{
		Name: "ZT_BAD", Description: "d", Package: "$TMP",
		Fields: []TableField{{Name: "ID", Type: "CHAR10", IsKey: true}, {Name: "MANDT", Type: "MANDT", IsKey: true}},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if n := len(rec.snapshot()); n != 0 {
		dumpCalls(t, rec.snapshot())
		t.Fatalf("%d request(s) were sent for an invalid specification", n)
	}
}
