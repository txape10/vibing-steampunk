package saprfc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oisee/open-rfc-go/rfc"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// The job log and a spool request by number come through XBP, the external
// background-processing interface: BP_JOBLOG_READ and RSPO_RETURN_*_SPOOLJOB
// cannot be called remotely. XBP wants an XMI logon first and a logoff after,
// on the same connection, so both reads pin a session (see rfcCaller).

// JobLogEntry is one line of a job log.
type JobLogEntry struct {
	Date    string `json:"date"`
	Time    string `json:"time"`
	Type    string `json:"type,omitempty"`
	ID      string `json:"id,omitempty"`
	No      string `json:"no,omitempty"`
	Text    string `json:"text"`
	Program string `json:"program,omitempty"`
}

// ReadJobLog reads a job's log. XBP only serves jobs that have started
// (running, finished or cancelled).
func ReadJobLog(ctx context.Context, c *rfc.Client, jobName, jobCount string) ([]JobLogEntry, error) {
	if !adt.ValidJobName(jobName) {
		return nil, fmt.Errorf("invalid job name %q: expected up to 32 letters, digits, or _ - /", jobName)
	}
	if !adt.ValidJobCount(jobCount) {
		return nil, fmt.Errorf("invalid job count %q: expected up to 8 digits", jobCount)
	}
	session, err := c.Pin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pinning an RFC session: %w", err)
	}
	defer session.Close()
	if err := xmiLogon(ctx, session); err != nil {
		return nil, err
	}
	defer func() { _, _ = session.Call(ctx, "BAPI_XMI_LOGOFF", rfc.Params{"INTERFACE": "XBP"}) }()

	res, err := session.Call(ctx, "BAPI_XBP_JOB_JOBLOG_READ", rfc.Params{
		"JOBNAME": jobName, "JOBCOUNT": jobCount, "EXTERNAL_USER_NAME": xbpUser, "PROT_NEW": "X",
	})
	if err != nil {
		return nil, fmt.Errorf("BAPI_XBP_JOB_JOBLOG_READ: %w", err)
	}
	if err := bapiError("BAPI_XBP_JOB_JOBLOG_READ", res.Get("RETURN")); err != nil {
		return nil, err
	}
	rows := res.Table("JOB_PROTOCOL_NEW")
	if len(rows) == 0 {
		rows = res.Table("JOB_PROTOCOL")
	}
	out := make([]JobLogEntry, 0, len(rows))
	for _, row := range rows {
		get := func(k string) string {
			if v, ok := row[k]; ok && v != nil {
				return strings.TrimSpace(fmt.Sprint(v))
			}
			return ""
		}
		out = append(out, JobLogEntry{Date: get("ENTERDATE"), Time: get("ENTERTIME"), Type: get("MSGTYPE"),
			ID: get("MSGID"), No: get("MSGNO"), Text: get("TEXT"), Program: get("PROGRAM")})
	}
	return out, nil
}

// ReadSpoolRequest reads a spool request by number as plain text lines,
// wherever TemSe keeps it. Unlike the SQL read, SAP applies the spool
// authorization of the RFC user.
func ReadSpoolRequest(ctx context.Context, c *rfc.Client, number int) ([]string, error) {
	if number <= 0 {
		return nil, fmt.Errorf("invalid spool request number %d", number)
	}
	session, err := c.Pin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pinning an RFC session: %w", err)
	}
	defer session.Close()
	if err := xmiLogon(ctx, session); err != nil {
		return nil, err
	}
	defer func() { _, _ = session.Call(ctx, "BAPI_XMI_LOGOFF", rfc.Params{"INTERFACE": "XBP"}) }()

	res, err := session.Call(ctx, "BAPI_XBP_GET_SPOOL_AS_DAT", rfc.Params{
		"SPOOL_REQUEST": number, "EXTERNAL_USER_NAME": xbpUser,
	})
	if err != nil {
		return nil, fmt.Errorf("BAPI_XBP_GET_SPOOL_AS_DAT: %w", err)
	}
	if err := bapiError("BAPI_XBP_GET_SPOOL_AS_DAT", res.Get("RETURN")); err != nil {
		return nil, err
	}
	// The spool is a table of strings, not of structures; it is reachable
	// through the result's JSON form, where it is an array.
	raw, err := res.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Lines []string `json:"SPOOL_LIST"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("BAPI_XBP_GET_SPOOL_AS_DAT: reading SPOOL_LIST: %w", err)
	}
	lines := make([]string, len(decoded.Lines))
	for i, l := range decoded.Lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines, nil
}
