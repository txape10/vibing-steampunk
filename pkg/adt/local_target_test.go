package adt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const stalePackageCheckXML = `<?xml version="1.0" encoding="utf-8"?><asx:abap version="1.0" xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA>` +
	`<DEVCLASS>ZABAP01</DEVCLASS><TADIRDEVC>ZABAP01</TADIRDEVC><RECORDING>X</RECORDING><REQUESTS/><LOCKS/></DATA></asx:values></asx:abap>`

const oneCandidateCheckXML = `<?xml version="1.0" encoding="utf-8"?><asx:abap version="1.0" xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA>` +
	`<DEVCLASS>ZDEMO</DEVCLASS><RECORDING>X</RECORDING><REQUESTS><CTS_REQUEST><REQ_HEADER><TRKORR>TR-EXAMPLE</TRKORR>` +
	`<TRFUNCTION>K</TRFUNCTION><TRSTATUS>D</TRSTATUS><AS4USER>TESTUSER</AS4USER><AS4TEXT>demo</AS4TEXT></REQ_HEADER></CTS_REQUEST></REQUESTS><LOCKS/></DATA></asx:values></asx:abap>`

func staleCreateClient(t *testing.T, checkXML string) (*Client, *adtRecorder) {
	t.Helper()
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "cts/transportchecks") {
			_, _ = io.WriteString(w, checkXML)
			return
		}
		w.WriteHeader(http.StatusOK)
	}, WithAllowedPackages("Z*", "$TMP"))
	return client, rec
}

func TestCreateObject_LocalPackage_RefusesNameStaleInTransportablePackage(t *testing.T) {
	client, rec := staleCreateClient(t, stalePackageCheckXML)
	err := client.CreateObject(context.Background(), CreateObjectOptions{
		ObjectType: ObjectTypeProgram, Name: "ZDEMO_STALE", PackageName: "$TMP", Description: "x",
	})
	if !errors.Is(err, errStaleTransportableTarget) {
		t.Fatalf("expected errStaleTransportableTarget, got %v", err)
	}
	for _, c := range rec.snapshot() {
		if c.method == http.MethodPost && strings.HasSuffix(c.path, "/programs/programs") {
			t.Errorf("creation POST was sent despite the stale TADIR entry")
		}
		if c.query.Get("_action") == "LOCK" {
			t.Errorf("a LOCK was sent")
		}
	}
}

func TestCreateObject_LocalPackage_FreshNameIsNotRefused(t *testing.T) {
	client, _ := staleCreateClient(t, localObjectCheckXML)
	err := client.CreateObject(context.Background(), CreateObjectOptions{
		ObjectType: ObjectTypeProgram, Name: "ZDEMO_FRESH", PackageName: "$TMP", Description: "x",
	})
	if errors.Is(err, errStaleTransportableTarget) {
		t.Fatalf("a name local in TADIR must not be refused: %v", err)
	}
}

func writeProgramWithLockOrder(t *testing.T, checkXML string) (*WriteProgramResult, error, *adtRecorder) {
	t.Helper()
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, checkXML)
		case strings.Contains(r.URL.Path, "/checkruns"):
			w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
			_, _ = io.WriteString(w, testEmptyCheckXML)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXMLWithTransport)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())
	res, err := client.WriteProgram(context.Background(), "ZDEMO_PROBE", "REPORT zdemo_probe.", "")
	return res, err, rec
}

func TestWriteProgram_LockReturnsRequestNobodyChose_IsNotAdopted(t *testing.T) {
	res, err, rec := writeProgramWithLockOrder(t, localObjectCheckXML)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if res != nil {
		msg += res.Message
	}
	if !strings.Contains(msg, ErrUnexpectedLockTransport.Error()) {
		t.Fatalf("expected ErrUnexpectedLockTransport, got err=%v res=%+v", err, res)
	}
	for _, c := range rec.snapshot() {
		if isSourcePut(c) {
			t.Errorf("source PUT sent under a request nobody chose")
		}
	}
}

func TestWriteProgram_LockReturnsPlannedRequest_IsAdopted(t *testing.T) {
	_, err, rec := writeProgramWithLockOrder(t, oneCandidateCheckXML)
	if err != nil {
		t.Fatalf("planned request must be accepted: %v", err)
	}
	at := indexOfCall(rec.snapshot(), isSourcePut)
	if at < 0 || rec.snapshot()[at].query.Get("corrNr") != "TR-EXAMPLE" {
		t.Fatalf("expected the source PUT under TR-EXAMPLE; trace %v", rec.snapshot())
	}
}

const noRecordingButLockedXML = `<?xml version="1.0" encoding="utf-8"?><asx:abap version="1.0" xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA>` +
	`<DEVCLASS>ZDEMO</DEVCLASS><RECORDING></RECORDING><REQUESTS/><LOCKS><CTS_OBJECT_LOCK><REQ_HEADER><TRKORR>TR-EXAMPLE</TRKORR></REQ_HEADER></CTS_OBJECT_LOCK></LOCKS></DATA></asx:values></asx:abap>`

func TestWriteProgram_ObjectAlreadyLockedInOpenRequest_IsAdoptedEvenWithoutRecording(t *testing.T) {
	_, err, rec := writeProgramWithLockOrder(t, noRecordingButLockedXML)
	if err != nil {
		t.Fatalf("an object already captured in an open request must be accepted: %v", err)
	}
	at := indexOfCall(rec.snapshot(), isSourcePut)
	if at < 0 || rec.snapshot()[at].query.Get("corrNr") != "TR-EXAMPLE" {
		t.Fatalf("expected the source PUT under TR-EXAMPLE; trace %v", rec.snapshot())
	}
}

func TestResolveWriteTransportFor_UnchosenLockRequest(t *testing.T) {
	c := NewClient("https://x.example", "u", "p", WithAllowTransportableEdits())
	cases := []struct {
		name    string
		plan    *TransportChoice
		lock    string
		wantErr bool
		want    string
	}{
		{"nil plan adopts (choice off or request named)", nil, "TR-EXAMPLE", false, "TR-EXAMPLE"},
		{"plan without request and lock request refuses", &TransportChoice{Reason: "local"}, "TR-EXAMPLE", true, ""},
		{"plan with request accepts lock request", &TransportChoice{Transport: "TR-EXAMPLE"}, "TR-EXAMPLE", false, "TR-EXAMPLE"},
		{"plan without request, no lock request, ok", &TransportChoice{}, "", false, ""},
	}
	for _, tc := range cases {
		got, _, err := c.resolveWriteTransportFor(tc.plan, "", tc.lock, "Test")
		if tc.wantErr != (err != nil) || got != tc.want {
			t.Errorf("%s: got %q err=%v", tc.name, got, err)
		}
		if tc.wantErr && !errors.Is(err, ErrUnexpectedLockTransport) {
			t.Errorf("%s: wrong error %v", tc.name, err)
		}
	}
}

func TestVerifyLocalTarget_FallbackToTadir(t *testing.T) {
	for _, tc := range []struct {
		name, devclass string
		wantErr        bool
	}{{"transportable row refuses", "ZABAP01", true}, {"local row passes", "$TMP", false}} {
		rec := &adtRecorder{}
		var sql string
		client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.Contains(r.URL.Path, "cts/transportchecks"):
				w.WriteHeader(http.StatusInternalServerError)
			case strings.Contains(r.URL.Path, "datapreview"):
				b, _ := io.ReadAll(r.Body)
				sql = string(b)
				_, _ = io.WriteString(w, `<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview"><dataPreview:totalRows>1</dataPreview:totalRows><dataPreview:columns><dataPreview:metadata dataPreview:name="DEVCLASS" dataPreview:type="C" dataPreview:description="d" dataPreview:keyAttribute="false" dataPreview:length="30"/><dataPreview:dataSet><dataPreview:data>`+tc.devclass+`</dataPreview:data></dataPreview:dataSet></dataPreview:columns></dataPreview:tableData>`)
			default:
				w.WriteHeader(http.StatusOK)
			}
		})
		err := client.verifyLocalTarget(context.Background(), "/sap/bc/adt/programs/programs/zdemo", "$TMP", "ZDEMO", "PROG/P")
		if tc.wantErr != (err != nil) {
			t.Errorf("%s: err=%v (sql %q)", tc.name, err, sql)
		}
		if !strings.Contains(sql, "object = 'PROG'") {
			t.Errorf("%s: TADIR query not filtered by type: %q", tc.name, sql)
		}
	}
}
