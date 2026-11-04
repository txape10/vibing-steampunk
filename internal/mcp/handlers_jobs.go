package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
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
