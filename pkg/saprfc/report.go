package saprfc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oisee/open-rfc-go/rfc"
)

// Running a report is the classic thing ADT cannot do: the APC WebSocket forbids
// SUBMIT (APC_ILLEGAL_STATEMENT), which is why issues about running reports from
// vsp were closed as an architectural limit. Over RFC there is no such ban — the
// XBP BAPIs schedule the report as a background job, TBTCO carries its status, and
// XBP returns the spool it produced.

// ReportParam is one selection-screen parameter or select-option line.
type ReportParam struct {
	Name   string `json:"name"`             // SELNAME, e.g. "P_MATNR" or "S_WERKS"
	Kind   string `json:"kind,omitempty"`   // "P" parameter, "S" select-option (default P)
	Sign   string `json:"sign,omitempty"`   // "I" include (default) or "E" exclude
	Option string `json:"option,omitempty"` // "EQ" (default), "BT", "CP", …
	Low    string `json:"low"`
	High   string `json:"high,omitempty"`
}

// rfcCaller is satisfied by both *rfc.Client (a pooled connection per call) and
// *rfc.Session (one connection pinned for a whole stateful conversation). The
// XMI/XBP sequence below needs a Session: SAP's XMI authorization (BAPI_XMI_LOGON)
// is tied to the physical connection that logged on, and a pooled Client.Call
// could hand a later step in the same sequence a different, unauthorized
// connection out of the pool.
type rfcCaller interface {
	Call(ctx context.Context, functionName string, in rfc.Params) (rfc.Result, error)
}

// JobRun is the outcome of scheduling a report.
type JobRun struct {
	Report    string `json:"report"`
	JobName   string `json:"job_name"`
	JobCount  string `json:"job_count"`
	Status    string `json:"status,omitempty"`      // TBTCO STATUS once known
	StatusFor string `json:"status_text,omitempty"` // human reading of that letter
	Spool     string `json:"spool,omitempty"`       // spool list, when requested and available
}

// jobStatusText turns a TBTCO status letter into words.
func jobStatusText(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "P":
		return "scheduled"
	case "S":
		return "released"
	case "R":
		return "running"
	case "F":
		return "finished"
	case "A":
		return "cancelled"
	case "Y":
		return "ready"
	case "":
		return ""
	}
	return "unknown"
}

// RunReport runs a report as a background job through the XBP interface.
//
// SUBST_START_REPORT_IN_BATCH looks like the obvious call, but it picks a batch
// server itself and fails with BATCH_SCHEDULING_FAILED (XM262) on systems where
// that selection does not resolve — a containerised A4H, for one, even with
// SAP_ALL and free batch work processes. The XBP BAPIs are the supported external
// scheduler interface: they take the target server explicitly, so they work where
// SUBST does not, and they carry a BAPIRET2 that says what went wrong.
func RunReport(ctx context.Context, c *rfc.Client, report, jobName string, params []ReportParam, wait time.Duration) (*JobRun, error) {
	report = strings.ToUpper(strings.TrimSpace(report))
	if report == "" {
		return nil, fmt.Errorf("a report name is required")
	}
	if jobName == "" {
		jobName = "VSP_" + report
	}
	if len(jobName) > 32 {
		jobName = jobName[:32]
	}

	// See rfcCaller's doc comment: the whole LOGON..LOGOFF sequence, including
	// the status poll below, must stay on one physical connection.
	session, err := c.Pin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pinning an RFC session: %w", err)
	}
	defer session.Close()

	if err := xmiLogon(ctx, session); err != nil {
		return nil, err
	}
	defer func() { _, _ = session.Call(ctx, "BAPI_XMI_LOGOFF", rfc.Params{"INTERFACE": "XBP"}) }()

	run := &JobRun{Report: report, JobName: jobName}

	opened, err := session.Call(ctx, "BAPI_XBP_JOB_OPEN", rfc.Params{
		"JOBNAME": jobName, "EXTERNAL_USER_NAME": xbpUser,
	})
	if err != nil {
		return nil, fmt.Errorf("BAPI_XBP_JOB_OPEN: %w", err)
	}
	if err := bapiError("BAPI_XBP_JOB_OPEN", opened.Get("RETURN")); err != nil {
		return nil, err
	}
	run.JobCount = strings.TrimSpace(fmt.Sprint(opened.Get("JOBCOUNT")))

	step := rfc.Params{
		"JOBNAME": jobName, "JOBCOUNT": run.JobCount,
		"EXTERNAL_USER_NAME": xbpUser, "ABAP_PROGRAM_NAME": report,
		// Without print parameters a background step writes its list nowhere and
		// TBTCP-LISTIDENT stays 0, so there is nothing to read afterwards.
		// ALLPRIPAR is the classic PRI_PARAMS set: hold the request on the default
		// device rather than printing it.
		"ALLPRIPAR": map[string]any{
			"PDEST": printDestination, // spool device
			"PRIMM": " ",              // do not print immediately
			"PRREL": " ",              // keep the request after printing
			"PEXPI": "8",              // days before it expires
			"LINSZ": 255,
			"LINCT": 65,
		},
	}
	if rows := selectionRows(params); len(rows) > 0 {
		step["SELINFO"] = rows
	}
	added, err := session.Call(ctx, "BAPI_XBP_JOB_ADD_ABAP_STEP", step)
	if err != nil {
		return run, fmt.Errorf("BAPI_XBP_JOB_ADD_ABAP_STEP: %w", err)
	}
	if err := bapiError("BAPI_XBP_JOB_ADD_ABAP_STEP", added.Get("RETURN")); err != nil {
		return run, err
	}

	server, err := applicationServer(ctx, session)
	if err != nil {
		return run, err
	}
	started, err := session.Call(ctx, "BAPI_XBP_JOB_START_ASAP", rfc.Params{
		"JOBNAME": jobName, "JOBCOUNT": run.JobCount,
		"EXTERNAL_USER_NAME": xbpUser, "TARGET_SERVER": server,
	})
	if err != nil {
		return run, fmt.Errorf("BAPI_XBP_JOB_START_ASAP: %w", err)
	}
	if err := bapiError("BAPI_XBP_JOB_START_ASAP", started.Get("RETURN")); err != nil {
		return run, err
	}

	if wait <= 0 {
		return run, nil
	}
	deadline := time.Now().Add(wait)
	for {
		status, err := jobStatus(ctx, session, run.JobName, run.JobCount)
		if err != nil {
			return run, err
		}
		run.Status, run.StatusFor = status, jobStatusText(status)
		// P scheduled, S released, R running, Y ready — anything else is terminal.
		if status != "" && !strings.Contains("PSRY", strings.ToUpper(status)) {
			return run, nil
		}
		if time.Now().After(deadline) {
			return run, nil // still running; the caller has the job id to follow up
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// JobStatus reads a scheduled job's current TBTCO status and its human reading,
// for callers that scheduled a job with wait<=0 (via RunReport) and want to check
// on it later without re-running RunReport.
func JobStatus(ctx context.Context, c *rfc.Client, jobName, jobCount string) (status, statusText string, err error) {
	status, err = jobStatus(ctx, c, jobName, jobCount)
	return status, jobStatusText(status), err
}

// xbpUser is the external scheduler identity XBP records against the job.
const xbpUser = "vsp"

// xmiLogon opens the XMI session the XBP BAPIs require.
func xmiLogon(ctx context.Context, c rfcCaller) error {
	res, err := c.Call(ctx, "BAPI_XMI_LOGON", rfc.Params{
		"EXTCOMPANY": "vsp", "EXTPRODUCT": "vibing-steampunk", "INTERFACE": "XBP", "VERSION": "3.0",
	})
	if err != nil {
		return fmt.Errorf("BAPI_XMI_LOGON: %w", err)
	}
	return bapiError("BAPI_XMI_LOGON", res.Get("RETURN"))
}

// applicationServer returns this instance's name, which XBP wants as the job's
// target server. RFC_SYSTEM_INFO reports it as RFCDEST (host_SID_nn).
func applicationServer(ctx context.Context, c rfcCaller) (string, error) {
	info, err := c.Call(ctx, "RFC_SYSTEM_INFO", nil)
	if err != nil {
		return "", fmt.Errorf("resolving the target server: %w", err)
	}
	m, _ := info.Get("RFCSI_EXPORT").(map[string]any)
	server := strings.TrimSpace(fmt.Sprint(m["RFCDEST"]))
	if server == "" {
		return "", fmt.Errorf("could not determine the application server name")
	}
	return server, nil
}

// selectionRows turns parameters into XBP's SELINFO rows.
func selectionRows(params []ReportParam) []map[string]any {
	rows := make([]map[string]any, 0, len(params))
	for _, p := range params {
		rows = append(rows, map[string]any{
			"SELNAME": strings.ToUpper(p.Name),
			"KIND":    firstNonEmpty(strings.ToUpper(p.Kind), "P"),
			"SIGN":    firstNonEmpty(strings.ToUpper(p.Sign), "I"),
			"OPTION":  firstNonEmpty(strings.ToUpper(p.Option), "EQ"),
			"LOW":     p.Low,
			"HIGH":    p.High,
		})
	}
	return rows
}

// bapiError turns a BAPIRET2 of type E or A into a Go error.
func bapiError(call string, ret any) error {
	m, ok := ret.(map[string]any)
	if !ok {
		return nil
	}
	t := strings.ToUpper(strings.TrimSpace(fmt.Sprint(m["TYPE"])))
	if t != "E" && t != "A" {
		return nil
	}
	msg := strings.TrimSpace(fmt.Sprint(m["MESSAGE"]))
	if msg == "" {
		msg = fmt.Sprintf("%v%v", m["ID"], m["NUMBER"])
	}
	return fmt.Errorf("%s: %s", call, msg)
}

// validJobName reports whether name is safe to interpolate literally into an
// RFC_READ_TABLE WHERE clause. TBTCO-JOBNAME is CHAR 32; SAP job names are
// conventionally uppercase alphanumeric plus a few separators — nothing in that
// set can close the quoted literal early.
func validJobName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '/':
		default:
			return false
		}
	}
	return true
}

// validJobCount reports whether count is a well-formed TBTCO-JOBCOUNT (an
// 8-digit NUMC field).
func validJobCount(count string) bool {
	if count == "" || len(count) > 8 {
		return false
	}
	for _, r := range count {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// jobStatus reads one job's TBTCO status. jobName/jobCount are validated
// against a character whitelist before being interpolated into the WHERE
// clause — RFC_READ_TABLE's OPTIONS has no bind parameters, so an unvalidated
// caller-controlled value here would be a classic WHERE-clause injection (the
// same class of bug already fixed in this codebase for RFC_SEARCH/TFDIR and
// GetUserTransports/AS4USER).
func jobStatus(ctx context.Context, c rfcCaller, jobName, jobCount string) (string, error) {
	if !validJobName(jobName) {
		return "", fmt.Errorf("invalid job name %q: expected up to 32 letters, digits, or _ - /", jobName)
	}
	if !validJobCount(jobCount) {
		return "", fmt.Errorf("invalid job count %q: expected up to 8 digits", jobCount)
	}
	where := fmt.Sprintf("JOBNAME = '%s' AND JOBCOUNT = '%s'", jobName, jobCount)
	r, err := c.Call(ctx, "RFC_READ_TABLE", rfc.Params{
		"QUERY_TABLE": "TBTCO", "DELIMITER": "|", "ROWCOUNT": int64(1),
		"OPTIONS": []map[string]any{{"TEXT": where}},
		"FIELDS":  []map[string]any{{"FIELDNAME": "STATUS"}},
	})
	if err != nil {
		return "", fmt.Errorf("reading job status: %w", err)
	}
	rows := r.Table("DATA")
	if len(rows) == 0 {
		return "", nil
	}
	// A single-field WA is still delimiter-joined (matches ReadTable's own
	// convention in readtable.go).
	parts := strings.Split(fmt.Sprint(rows[0]["WA"]), "|")
	if len(parts) == 0 {
		return "", nil
	}
	return strings.TrimRight(parts[0], " "), nil
}

// printDestination is the spool device a step prints to. LP01 exists on every
// system; the request is held rather than printed.
const printDestination = "LP01"

// ReadSpool returns a finished job's spool list through the XBP BAPIs, which need
// their own XMI session — hence the logon and logoff around the read. A job that
// produced no spool (TBTCP-LISTIDENT is zero) yields an empty string, not an error.
func ReadSpool(ctx context.Context, c *rfc.Client, jobName, jobCount string) (string, error) {
	return ReadSpoolStep(ctx, c, jobName, jobCount, 1)
}

// ReadSpoolStep reads the spool list of one step of a job.
func ReadSpoolStep(ctx context.Context, c *rfc.Client, jobName, jobCount string, step int) (string, error) {
	// Same reasoning as RunReport: LOGON and LOGOFF must be the same connection.
	session, err := c.Pin(ctx)
	if err != nil {
		return "", fmt.Errorf("pinning an RFC session: %w", err)
	}
	defer session.Close()

	if err := xmiLogon(ctx, session); err != nil {
		return "", err
	}
	defer func() { _, _ = session.Call(ctx, "BAPI_XMI_LOGOFF", rfc.Params{"INTERFACE": "XBP"}) }()

	res, err := session.Call(ctx, "BAPI_XBP_JOB_SPOOLLIST_READ", rfc.Params{
		"JOBNAME": jobName, "JOBCOUNT": jobCount, "EXTERNAL_USER_NAME": xbpUser,
		"STEP_NUMBER": step, // XBP requires it
	})
	if err != nil {
		return "", fmt.Errorf("BAPI_XBP_JOB_SPOOLLIST_READ: %w", err)
	}
	var b strings.Builder
	for _, row := range res.Table("SPOOL_LIST") {
		for _, key := range []string{"LINE", "SPOOLLIST"} {
			if v, ok := row[key]; ok {
				fmt.Fprintln(&b, strings.TrimRight(fmt.Sprint(v), " "))
				break
			}
		}
	}
	return b.String(), nil
}
