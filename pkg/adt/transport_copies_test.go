package adt

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A transport of copies is a request of type T. It must come back in its own
// group, wherever the organizer tree nests it, and not as a workbench request.
const treeWithCopy = `<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">
  <tm:workbench tm:desc="Workbench">
    <tm:target tm:name="TGT">
      <tm:modifiable>
        <tm:request tm:number="TR-EXAMPLE-1" tm:owner="TESTUSER" tm:desc="Normal" tm:type="K" tm:status="D"/>
        <tm:request tm:number="TR-EXAMPLE-COPY" tm:owner="TESTUSER" tm:desc="Copy" tm:type="T" tm:status="D"/>
      </tm:modifiable>
    </tm:target>
  </tm:workbench>
</tm:root>`

func TestParseUserTransports_CopyGoesToItsOwnGroup(t *testing.T) {
	got, err := parseUserTransports([]byte(treeWithCopy))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Workbench) != 1 || got.Workbench[0].Number != "TR-EXAMPLE-1" {
		t.Errorf("workbench = %+v, want only TR-EXAMPLE-1", got.Workbench)
	}
	if len(got.TransportOfCopies) != 1 || got.TransportOfCopies[0].Number != "TR-EXAMPLE-COPY" {
		t.Fatalf("transportOfCopies = %+v, want TR-EXAMPLE-COPY", got.TransportOfCopies)
	}
	if got.TransportOfCopies[0].Target != "TGT" || got.TransportOfCopies[0].Status != "D" {
		t.Errorf("copy lost its target/status: %+v", got.TransportOfCopies[0])
	}
}

func TestParseUserTransports_OnlyACopyIsNotEmpty(t *testing.T) {
	const onlyCopy = `<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"><tm:request tm:number="TR-EXAMPLE-COPY" tm:owner="TESTUSER" tm:type="T" tm:status="D"/></tm:root>`
	got, err := parseUserTransports([]byte(onlyCopy))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TransportOfCopies) != 1 || len(got.Workbench) != 0 {
		t.Errorf("got %+v", got)
	}
}

// ADT lists released requests of the last two weeks only when requestStatus is
// missing, and leaves out transports of copies when requestType is.
func TestListings_AskForTypeAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(c *Client) error
	}{
		{"GetUserTransports", func(c *Client) error { _, err := c.GetUserTransports(context.Background(), "TESTUSER"); return err }},
		{"ListTransports", func(c *Client) error { _, err := c.ListTransports(context.Background(), "TESTUSER"); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &adtRecorder{}
			client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, treeWithCopy)
			}, WithEnableTransports())
			if err := tc.call(client); err != nil {
				t.Fatal(err)
			}
			i := indexOfCall(rec.snapshot(), func(c wireCall) bool { return c.path == "/sap/bc/adt/cts/transportrequests" })
			if i < 0 {
				t.Fatal("the listing was never requested")
			}
			q := rec.snapshot()[i].query
			if q.Get("requestType") != "KWT" || q.Get("requestStatus") != "D" {
				t.Errorf("query = %v, want requestType=KWT and requestStatus=D", q)
			}
		})
	}
}

func TestTransportSQLFallback_AsksForCopiesAndSessionLanguage(t *testing.T) {
	var sql string
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/datapreview/freestyle" {
			b, _ := io.ReadAll(r.Body)
			sql = string(b)
		}
		_, _ = io.WriteString(w, `<?xml version="1.0"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview"/>`)
	}, WithEnableTransports(), WithLanguage("ES"))
	if _, err := client.userTransportsViaSQL(context.Background(), "TESTUSER"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "'T'") {
		t.Errorf("the E070 fallback does not ask for transports of copies: %s", sql)
	}
	if !strings.Contains(sql, "LANGU = 'S'") {
		t.Errorf("request texts are not read in the session language: %s", sql)
	}
}

func TestE07tLanguage(t *testing.T) {
	for in, want := range map[string]string{"ES": "S", "EN": "E", "": "E", "DE": "D", "S": "S"} {
		if got := e07tLanguage(in); got != want {
			t.Errorf("e07tLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
