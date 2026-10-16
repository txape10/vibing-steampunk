package adt

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A generic .abap file used to recurse ParseABAPFile -> parseFromContent ->
// ParseABAPFile until the stack overflowed, which kills the whole process.
func TestParseABAPFile_GenericDotAbap(t *testing.T) {
	cases := []struct {
		file, content, wantName string
		wantType                CreatableObjectType
	}{
		{"zdemo.abap", "REPORT zdemo.\nWRITE 'x'.\n", "ZDEMO", ObjectTypeProgram},
		{"zcl_demo.abap", "CLASS zcl_demo DEFINITION PUBLIC.\nENDCLASS.\n", "ZCL_DEMO", ObjectTypeClass},
		{"zif_demo.abap", "INTERFACE zif_demo PUBLIC.\nENDINTERFACE.\n", "ZIF_DEMO", ObjectTypeInterface},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.file)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			type result struct {
				info *ABAPFileInfo
				err  error
			}
			done := make(chan result, 1)
			go func() {
				info, err := ParseABAPFile(path)
				done <- result{info, err}
			}()
			select {
			case r := <-done:
				if r.err != nil {
					t.Fatalf("ParseABAPFile: %v", r.err)
				}
				if r.info.ObjectType != tc.wantType || r.info.ObjectName != tc.wantName {
					t.Errorf("got %s %s, want %s %s", r.info.ObjectType, r.info.ObjectName, tc.wantType, tc.wantName)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("ParseABAPFile did not return")
			}
		})
	}
}

func TestParseABAPFile_GenericDotAbapWithoutStatementFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.abap")
	if err := os.WriteFile(path, []byte("* just a comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseABAPFile(path); err == nil {
		t.Fatal("expected an error for a file without a recognizable statement")
	}
}
