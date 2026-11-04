package mcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// Background jobs (SM37) over free SQL: TBTCO for the job, TBTCP for its steps.

// routeJobsAction routes "analyze" with the job types.
func (s *Server) routeJobsAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action != "analyze" {
		return nil, false, nil
	}
	switch getStringParam(params, "type") {
	case "job_list":
		return s.callHandler(ctx, s.handleJobList, params)
	case "spool_list":
		return s.callHandler(ctx, s.handleSpoolList, params)
	case "spool_read":
		return s.callHandler(ctx, s.handleSpoolRead, params)
	case "job_log":
		return s.callHandler(ctx, s.handleJobLog, params)
	}
	return nil, false, nil
}

type jobListResult struct {
	Jobs  []adt.Job `json:"jobs"`
	Count int       `json:"count"`
	Notes []string  `json:"notes,omitempty"`
}

func (s *Server) handleJobList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	filter := adt.JobFilter{
		Name:    firstStringParam(args, "name", "job", "job_name"),
		User:    getStringParam(args, "user"),
		Status:  getStringParam(args, "status"),
		Program: getStringParam(args, "program"),
	}
	if n, ok := firstFloatParam(args, "max_results", "top", "limit"); ok && n > 0 {
		filter.Limit = int(n)
	}
	var err error
	if filter.From, err = jobDateParam(args, "from", "since", "date_from"); err != nil {
		return newToolResultError(err.Error()), nil
	}
	if filter.To, err = jobDateParam(args, "to", "until", "date_to"); err != nil {
		return newToolResultError(err.Error()), nil
	}
	jobs, err := s.adtClient.Jobs(ctx, filter)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to list jobs: %v", err)), nil
	}
	res := jobListResult{Jobs: jobs, Count: len(jobs)}
	if filter.Name == "" && filter.User == "" && filter.Status == "" && filter.Program == "" && filter.From.IsZero() && filter.To.IsZero() {
		res.Notes = []string{"No filter given: showing the last 7 days; pass since/from to look further back."}
	}
	return newToolResultJSON(res), nil
}

func firstStringParam(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(getStringParam(args, k)); v != "" {
			return v
		}
	}
	return ""
}

func firstFloatParam(args map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		if f, ok := getFloatParam(args, k); ok {
			return f, true
		}
	}
	return 0, false
}

// jobDateParam reads a YYYY-MM-DD or YYYYMMDD date from the first key present.
func jobDateParam(args map[string]any, keys ...string) (time.Time, error) {
	v := firstStringParam(args, keys...)
	if v == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q: use YYYY-MM-DD", v)
}

const noteSpoolInFiles = "This spool request's content is kept in a file on the application server, not in TST03; read it with via=\"rfc\" (XBP)."

type spoolListResult struct {
	Requests []adt.SpoolRequest `json:"requests"`
	Count    int                `json:"count"`
	Notes    []string           `json:"notes,omitempty"`
}

func (s *Server) handleSpoolList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	filter := adt.SpoolFilter{
		Owner:   firstStringParam(args, "user", "owner"),
		Title:   firstStringParam(args, "title"),
		Program: firstStringParam(args, "program"),
		Job:     firstStringParam(args, "job", "job_name"),
	}
	if n, ok := firstFloatParam(args, "max_results", "top", "limit"); ok && n > 0 {
		filter.Limit = int(n)
	}
	var err error
	if filter.From, err = jobDateParam(args, "from", "since", "date_from"); err != nil {
		return newToolResultError(err.Error()), nil
	}
	if filter.To, err = jobDateParam(args, "to", "until", "date_to"); err != nil {
		return newToolResultError(err.Error()), nil
	}
	reqs, err := s.adtClient.SpoolRequests(ctx, filter)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to list spool requests: %v", err)), nil
	}
	if reqs == nil {
		reqs = []adt.SpoolRequest{}
	}
	res := spoolListResult{Requests: reqs, Count: len(reqs)}
	if filter.Owner == "" && filter.Title == "" && filter.Program == "" && filter.Job == "" && filter.From.IsZero() && filter.To.IsZero() {
		res.Notes = append(res.Notes, "No filter given: showing the last 7 days; pass since/from to look further back.")
	}
	for _, r := range reqs {
		if r.Storage != "" && r.Storage != "D" {
			res.Notes = append(res.Notes, noteSpoolInFiles)
			break
		}
	}
	return newToolResultJSON(res), nil
}

type spoolReadResult struct {
	Number  int              `json:"number"`
	Request adt.SpoolRequest `json:"request"`
	Temse   *adt.TemSeHeader `json:"temse,omitempty"`
	Pages   int              `json:"pages,omitempty"`
	Text    string           `json:"text,omitempty"`
	Notes   []string         `json:"notes,omitempty"`
}

func (s *Server) handleSpoolRead(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	number := 0
	if n, ok := firstFloatParam(args, "number", "spool", "rqident"); ok {
		number = int(n)
	} else if v := firstStringParam(args, "number", "spool", "rqident"); v != "" {
		number, _ = strconv.Atoi(v)
	}
	if number <= 0 {
		return newToolResultError("spool_read needs number: the spool request number"), nil
	}
	via := strings.ToLower(firstStringParam(args, "via"))
	switch via {
	case "", "auto", "sql", "rfc":
	default:
		return newToolResultError(fmt.Sprintf("via %q is not valid: use auto, sql or rfc", via)), nil
	}
	if via == "rfc" {
		return s.spoolReadRFC(ctx, number)
	}
	content, err := s.adtClient.Spool(ctx, number)
	if err != nil {
		inFile := content != nil && content.Request.Storage != "" && content.Request.Storage != "D"
		if inFile && via != "sql" {
			if refused := s.refuseUnderSafety(adt.OpWorkflow, "SpoolReadRFC"); refused != nil {
				return newToolResultError(fmt.Sprintf("Spool request %d is kept in a file, which only XBP over RFC can read, and RFC is blocked by the safety configuration.", number)), nil
			}
			return s.spoolReadRFC(ctx, number)
		}
		if inFile {
			return newToolResultError(fmt.Sprintf("Failed to read spool request %d: %v. %s", number, err, noteSpoolInFiles)), nil
		}
		return newToolResultError(fmt.Sprintf("Failed to read spool request %d: %v", number, err)), nil
	}
	res := spoolReadResult{Number: number, Request: content.Request, Temse: content.Header}
	switch {
	case content.List != nil:
		res.Pages = content.List.Pages
		res.Text = content.List.Text()
	default:
		res.Notes = append(res.Notes, fmt.Sprintf("Document type %q is not an ABAP list; its %d bytes are not shown.", content.Request.DocType, len(content.Raw)))
	}
	return newToolResultJSON(res), nil
}

type spoolReadRFCResult struct {
	Number int      `json:"number"`
	Via    string   `json:"via"`
	Lines  []string `json:"lines"`
}

// spoolReadRFC reads a spool request through XBP. The RFC user's own spool
// authorization applies, and a read-only connection refuses it.
func (s *Server) spoolReadRFC(ctx context.Context, number int) (*mcp.CallToolResult, error) {
	if refused := s.refuseUnderSafety(adt.OpWorkflow, "SpoolReadRFC"); refused != nil {
		return refused, nil
	}
	c, err := s.ensureRFCClient(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to read spool request %d over RFC: %v", number, err)), nil
	}
	lines, err := saprfc.ReadSpoolRequest(ctx, c, number)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to read spool request %d over RFC: %v", number, err)), nil
	}
	return newToolResultJSON(spoolReadRFCResult{Number: number, Via: "rfc", Lines: lines}), nil
}

type jobLogResult struct {
	Job     string               `json:"job"`
	Count   string               `json:"count"`
	Entries []saprfc.JobLogEntry `json:"entries"`
	Notes   []string             `json:"notes,omitempty"`
}

func (s *Server) handleJobLog(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	name := strings.ToUpper(firstStringParam(args, "job", "job_name", "name"))
	count := firstStringParam(args, "count", "job_count", "jobcount")
	if name == "" || count == "" {
		return newToolResultError("job_log needs job (the name) and count (the job count from job_list)"), nil
	}
	if !adt.ValidJobName(name) {
		return newToolResultError(fmt.Sprintf("invalid job name %q: expected up to 32 letters, digits, or _ - /", name)), nil
	}
	if !adt.ValidJobCount(count) {
		return newToolResultError(fmt.Sprintf("invalid job count %q: expected up to 8 digits", count)), nil
	}
	if refused := s.refuseUnderSafety(adt.OpWorkflow, "JobLog"); refused != nil {
		return refused, nil
	}
	c, err := s.ensureRFCClient(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to read the job log over RFC: %v", err)), nil
	}
	entries, err := saprfc.ReadJobLog(ctx, c, name, count)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to read the job log of %s/%s: %v", name, count, err)), nil
	}
	return newToolResultJSON(jobLogResult{Job: name, Count: count, Entries: entries,
		Notes: []string{"Read over RFC through XBP; only jobs that have started have a log."}}), nil
}
