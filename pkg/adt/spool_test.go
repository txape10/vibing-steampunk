package adt

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

var spoolCols = []string{"RQIDENT", "RQCLIENT", "RQOWNER", "RQCRETIME", "RQTITLE", "RQ0NAME", "RQ1NAME", "RQ2NAME", "RQDOCTYPE", "RQDEST", "RQCOPIES", "RQPJREQ", "RQO1NAME"}
var temseCols = []string{"DNAME", "DTYPE", "DRECTYP", "DCHARCOD", "DSTOTYP", "DROWS", "DSIZE", "DLINELEN", "DNOPARTS", "DCREATER"}

// listHex is one list record " HELLO" in code page 4103 (UTF-16 little endian).
func listHex() string {
	body := []byte{' ', 0, 'H', 0, 'E', 0, 'L', 0, 'L', 0, 'O', 0}
	return "00100000" + hex.EncodeToString(body)
}

func spoolStub(storage string) *transactionStub {
	return &transactionStub{tables: map[string]tableAnswer{
		"TSP01": {columns: spoolCols, rows: [][]string{
			{"4388 ", "100", "TESTUSER", "2026110408091300", "", "LIST1S", "LP01", "ZDEMO_RUN_TES", "LIST", "LP01", "1 ", "0 ", "SPOOL0000004388"},
			{"4389 ", "100", "TESTUSER", "2026110408091400", "", "LIST1S", "LP01", "ZOTHER", "LIST", "LP01", "1 ", "0 ", "SPOOL0000004389"},
		}},
		"TST01": {columns: temseCols, rows: [][]string{
			{"SPOOL0000004388", "TEXT", "VY4----", "4103", storage, "1 ", "12 ", "108 ", "1 ", "TESTUSER"},
		}},
		"TST03": {columns: []string{"DROWNO", "DDATALEN", "DCONTENT"}, rows: [][]string{{"1 ", "16 ", listHex()}}},
		"TBTCP": {columns: []string{"JOBNAME", "JOBCOUNT", "STEPCOUNT", "PROGNAME", "VARIANT", "AUTHCKNAM", "LISTIDENT"}, rows: [][]string{
			{"ZDEMO_JOB", "09091200", "001", "ZDEMO_RUN", "", "TESTUSER", "0000004388"},
		}},
	}}
}

func TestSpoolRequests_MapsRowsAndJobs(t *testing.T) {
	s := spoolStub("D")
	reqs, err := s.client(t, WithClient("100")).SpoolRequests(context.Background(), SpoolFilter{Owner: "testuser"})
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d", len(reqs))
	}
	r := reqs[0]
	if r.Number != 4388 || r.Owner != "TESTUSER" || r.TemSe != "SPOOL0000004388" || r.Storage != "D" || r.Codepage != "4103" || r.Bytes != 12 {
		t.Errorf("request: %+v", r)
	}
	if r.Job == nil || r.Job.Name != "ZDEMO_JOB" || r.Job.Step != 1 {
		t.Errorf("job: %+v", r.Job)
	}
	if !strings.Contains(s.bodies[0], "rqclient = '100'") {
		t.Errorf("no client restriction: %s", s.bodies[0])
	}
	if !strings.Contains(s.bodies[0], "rqowner = 'TESTUSER'") {
		t.Errorf("query: %s", s.bodies[0])
	}
}

func TestSpoolRequests_NoFilterDefaultsToSevenDays(t *testing.T) {
	s := spoolStub("D")
	if _, err := s.client(t, WithClient("100")).SpoolRequests(context.Background(), SpoolFilter{}); err != nil {
		t.Fatal(err)
	}
	want := time.Now().Add(-spoolDefaultWindow).Format("20060102")
	if q := s.bodies[0]; !strings.Contains(q, "rqcretime >= '"+want) {
		t.Errorf("query: %s", q)
	}
}

func TestSpoolRequests_ProgramIsPrefixAndExactInGo(t *testing.T) {
	s := spoolStub("D")
	reqs, err := s.client(t, WithClient("100")).SpoolRequests(context.Background(), SpoolFilter{Program: "zdemo_run"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.bodies[0], "rq2name LIKE 'ZDEMO_RUN%'") {
		t.Errorf("query: %s", s.bodies[0])
	}
	if len(reqs) != 1 || reqs[0].Number != 4388 {
		t.Errorf("got %+v", reqs)
	}
}

func TestSpoolRequests_RejectsInjectionBeforeAnyRequest(t *testing.T) {
	bad := []SpoolFilter{
		{Owner: "A' OR '1'='1"}, {Title: "x' OR '1'='1"}, {Program: "ZP' OR 1=1"}, {Job: "J' OR 1=1"},
		{From: time.Now(), To: time.Now().Add(-48 * time.Hour)},
	}
	for _, f := range bad {
		s := spoolStub("D")
		if _, err := s.client(t, WithClient("100")).SpoolRequests(context.Background(), f); err == nil {
			t.Errorf("%+v accepted", f)
		}
		if s.requests != 0 {
			t.Errorf("%+v sent %d requests", f, s.requests)
		}
	}
}

func TestSpool_DecodesList(t *testing.T) {
	s := spoolStub("D")
	got, err := s.client(t, WithClient("100")).Spool(context.Background(), 4388)
	if err != nil {
		t.Fatal(err)
	}
	if got.List == nil || !strings.Contains(got.List.Text(), "HELLO") {
		t.Fatalf("list: %+v", got.List)
	}
	if got.Request.Job == nil {
		t.Error("job reference missing")
	}
	if q := s.bodies[len(s.bodies)-1]; !strings.Contains(q, "dpart = 1") || !strings.Contains(q, "BETWEEN 1 AND 2147483647") {
		t.Errorf("tst03 query: %s", q)
	}
}

func TestSpool_FileStorageIsExplained(t *testing.T) {
	s := spoolStub("F")
	got, err := s.client(t, WithClient("100")).Spool(context.Background(), 4388)
	if err == nil || !strings.Contains(err.Error(), "XBP") {
		t.Fatalf("err = %v", err)
	}
	if got == nil || got.Request.Storage != "F" {
		t.Errorf("content: %+v", got)
	}
	for _, r := range s.reads {
		if r == "TST03" {
			t.Error("TST03 read for file storage")
		}
	}
}

func TestTemSeObject_TooBigAndBadName(t *testing.T) {
	s := spoolStub("D")
	s.tables["TST01"] = tableAnswer{columns: temseCols, rows: [][]string{{"SPOOL0000004388", "TEXT", "", "4103", "D", "1 ", "30000000 ", "108 ", "1 ", "X"}}}
	if _, _, err := s.client(t).TemSeObject(context.Background(), "SPOOL0000004388"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("err = %v", err)
	}
	s2 := spoolStub("D")
	if _, _, err := s2.client(t).TemSeObject(context.Background(), "X' OR 1=1"); err == nil || s2.requests != 0 {
		t.Errorf("err = %v requests = %d", err, s2.requests)
	}
}

func TestDecodeHexCell(t *testing.T) {
	if b, err := decodeHexCell(" 00ff "); err != nil || len(b) != 2 || b[1] != 0xff {
		t.Errorf("%v %v", b, err)
	}
	for _, bad := range []string{"0", "zz"} {
		if _, err := decodeHexCell(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
