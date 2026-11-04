package adt

import (
	"fmt"
	"context"
	"strings"
	"testing"
	"time"
)

var jobCols = []string{"JOBNAME", "JOBCOUNT", "STATUS", "SDLDATE", "SDLTIME", "RELDATE", "RELTIME", "STRTDATE", "STRTTIME", "ENDDATE", "ENDTIME", "SDLUNAME", "REAXSERVER", "EXECSERVER", "PERIODIC", "JOBCLASS", "JOBLOG"}
var stepCols = []string{"JOBNAME", "JOBCOUNT", "STEPCOUNT", "PROGNAME", "VARIANT", "AUTHCKNAM", "LANGUAGE", "STATUS", "LISTIDENT", "XPGPROG", "EXTCMD"}

func jobsStub() *transactionStub {
	return &transactionStub{tables: map[string]tableAnswer{
		"TBTCO": {columns: jobCols, rows: [][]string{
			{"ZDEMO_NIGHTLY", "09091200", "F", "20260901", "220000", "20260901", "220001", "20260901", "220002", "20260901", "221502", "TESTUSER", "", "srv_S4D_00", "X", "C", "BIN001"},
			{"ZDEMO_OTHER", "09091201", "A", "20260901", "230000", "00000000", "000000", "00000000", "000000", "00000000", "000000", "TESTUSER", "", "", "", "C", ""},
		}},
		"TBTCP": {columns: stepCols, rows: [][]string{
			{"ZDEMO_NIGHTLY", "09091200", "001", "ZDEMO_RUN", "VAR1", "TESTUSER", "S", "F", "  27302", "", ""},
			{"SOMEONE_ELSE", "09091200", "001", "ZX", "", "", "", "", "0", "", ""}, // same count, other name
		}},
	}}
}

func jobsClient(t *testing.T, s *transactionStub) *Client {
	return s.client(t, WithClient("100"))
}

func TestJobs_MapsRowsAndSteps(t *testing.T) {
	s := jobsStub()
	jobs, err := jobsClient(t, s).Jobs(context.Background(), JobFilter{Name: "ZDEMO*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	j := jobs[0]
	if j.StatusText != "finished" || !j.Periodic || j.Server != "srv_S4D_00" || j.Duration != "15m0s" {
		t.Errorf("job: %+v", j)
	}
	if len(j.Steps) != 1 || j.Steps[0].Program != "ZDEMO_RUN" || j.Steps[0].Spool != 27302 || j.Steps[0].Variant != "VAR1" {
		t.Errorf("steps: %+v", j.Steps)
	}
	if !jobs[1].Started.IsZero() || len(jobs[1].Steps) != 0 {
		t.Errorf("unstarted job: %+v", jobs[1])
	}
	if got := strings.Join(s.reads, ","); got != "TBTCO,TBTCP" {
		t.Errorf("reads: %s", got)
	}
}

func TestJobs_ClientRestrictionAndWildcard(t *testing.T) {
	s := jobsStub()
	if _, err := jobsClient(t, s).Jobs(context.Background(), JobFilter{Name: "ZD*+"}); err != nil {
		t.Fatal(err)
	}
	q := s.bodies[0]
	if !strings.Contains(q, "authckman = '100'") || !strings.Contains(q, "jobname LIKE 'ZD%_'") {
		t.Errorf("query: %s", q)
	}
	if strings.Contains(q, "BETWEEN") {
		t.Errorf("a name filter must not add the default window: %s", q)
	}
}

func TestJobs_NoFilterDefaultsToSevenDays(t *testing.T) {
	s := jobsStub()
	if _, err := jobsClient(t, s).Jobs(context.Background(), JobFilter{}); err != nil {
		t.Fatal(err)
	}
	want := time.Now().Add(-jobsDefaultWindow).Format("20060102")
	if q := s.bodies[0]; !strings.Contains(q, "sdldate BETWEEN '"+want+"' AND '99991231'") {
		t.Errorf("query: %s", q)
	}
}

func TestJobs_RejectsInjectionBeforeAnyRequest(t *testing.T) {
	bad := []JobFilter{
		{Name: "X' OR 1=1 --"}, {User: "A' OR '1'='1"}, {Status: "F' OR '1"}, {Status: "Q"},
		{Program: "ZP' OR '1'='1"}, {Name: strings.Repeat("A", 33)},
	}
	for _, f := range bad {
		s := jobsStub()
		if _, err := jobsClient(t, s).Jobs(context.Background(), f); err == nil {
			t.Errorf("%+v accepted", f)
		}
		if s.requests != 0 {
			t.Errorf("%+v: %d requests reached the system", f, s.requests)
		}
	}
}

func TestJobs_NeedsNumericClient(t *testing.T) {
	s := jobsStub()
	c := s.client(t, WithClient("ABC"))
	if _, err := c.Jobs(context.Background(), JobFilter{}); err == nil {
		t.Error("missing client accepted")
	}
}

func TestJobs_ProgramReadsStepsFirst(t *testing.T) {
	s := jobsStub()
	s.tables["TBTCP"] = tableAnswer{columns: stepCols, rows: [][]string{
		{"ZDEMO_NIGHTLY", "09091200", "001", "ZDEMO_RUN", "", "", "", "", "0", "", ""},
	}}
	jobs, err := jobsClient(t, s).Jobs(context.Background(), JobFilter{Program: "zdemo_run"})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Name != "ZDEMO_NIGHTLY" {
		t.Fatalf("jobs: %+v", jobs)
	}
	if got := strings.Join(s.reads, ","); got != "TBTCP,TBTCO,TBTCP" {
		t.Errorf("reads: %s", got)
	}
	if !strings.Contains(s.bodies[0], "progname = 'ZDEMO_RUN'") || !strings.Contains(s.bodies[1], "jobname IN ( 'ZDEMO_NIGHTLY' )") {
		t.Errorf("bodies: %q", s.bodies)
	}
}

func TestJobs_ProgramWithNoStepsGivesNoJobs(t *testing.T) {
	s := &transactionStub{tables: map[string]tableAnswer{"TBTCP": {columns: stepCols}}}
	jobs, err := jobsClient(t, s).Jobs(context.Background(), JobFilter{Program: "ZNOPE"})
	if err != nil || len(jobs) != 0 {
		t.Fatalf("got %v, %v", jobs, err)
	}
	if got := strings.Join(s.reads, ","); got != "TBTCP" {
		t.Errorf("reads: %s", got)
	}
}

func TestParseSAPStamp(t *testing.T) {
	if !parseSAPStamp("00000000", "000000").IsZero() || !parseSAPStamp("", "").IsZero() {
		t.Error("initial date must be the zero time")
	}
	if got := parseSAPStamp("20260901", "221502"); got.Format("2006-01-02 15:04:05") != "2026-09-01 22:15:02" {
		t.Errorf("got %v", got)
	}
}

func TestJobGlob(t *testing.T) {
	g := jobGlob("Z_JOB*")
	if !g.MatchString("Z_JOB_1") || g.MatchString("ZXJOB1") {
		t.Error("underscore must be literal in the glob")
	}
	if !jobGlob("A+C").MatchString("ABC") || jobGlob("A+C").MatchString("ABBC") {
		t.Error("+ is exactly one character")
	}
}

func TestJobs_PeriodMustBeOrdered(t *testing.T) {
	s := jobsStub()
	now := time.Now()
	_, err := jobsClient(t, s).Jobs(context.Background(), JobFilter{From: now, To: now.Add(-48 * time.Hour)})
	if err == nil || s.requests != 0 {
		t.Errorf("err=%v requests=%d", err, s.requests)
	}
}

func TestJobs_StepQueryNamesTheJobsAndHandlesManyBlocks(t *testing.T) {
	s := jobsStub()
	var rows [][]string
	for i := 0; i < 120; i++ {
		rows = append(rows, []string{"J" + strings.Repeat("A", i%3), fmt.Sprintf("%08d", i), "F", "20260901", "220000", "00000000", "000000", "20260901", "220002", "20260901", "220003", "TESTUSER", "", "", "", "C", ""})
	}
	s.tables["TBTCO"] = tableAnswer{columns: jobCols, rows: rows}
	if _, err := jobsClient(t, s).Jobs(context.Background(), JobFilter{Limit: 200, Name: "J*"}); err != nil {
		t.Fatal(err)
	}
	steps := 0
	for _, b := range s.bodies {
		if strings.Contains(b, "FROM tbtcp") {
			steps++
			if !strings.Contains(b, "jobname IN (") {
				t.Errorf("step query without job names: %s", b)
			}
		}
	}
	if steps != 3 {
		t.Errorf("120 jobs should need 3 step blocks of 50, got %d", steps)
	}
}
