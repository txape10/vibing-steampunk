// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_report.go contains handlers for report execution and text elements.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// routeReportAction routes "debug" with report-related sub-actions.
func (s *Server) routeReportAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action != "debug" {
		return nil, false, nil
	}
	switch objectType {
	case "RUN_REPORT":
		return s.callHandler(ctx, s.handleRunReport, params)
	case "RUN_REPORT_ASYNC":
		return s.callHandler(ctx, s.handleRunReportAsync, params)
	case "GET_ASYNC_RESULT":
		return s.callHandler(ctx, s.handleGetAsyncResult, params)
	case "GET_REPORT_JOB_STATUS":
		return s.callHandler(ctx, s.handleGetReportJobStatus, params)
	case "GET_VARIANTS":
		return s.callHandler(ctx, s.handleGetVariants, params)
	case "GET_TEXT_ELEMENTS":
		return s.callHandler(ctx, s.handleGetTextElements, params)
	case "SET_TEXT_ELEMENTS":
		return s.callHandler(ctx, s.handleSetTextElements, params)
	}
	return nil, false, nil
}

// --- Report Execution Handlers ---
//
// RunReport/RunReportAsync go over classic RFC (pkg/saprfc, the XBP background-job
// BAPIs), not the ZADT_VSP WebSocket — the WebSocket's SUBMIT ... AND RETURN is
// illegal inside a stateful APC handler (APC_ILLEGAL_STATEMENT) on any report with
// a selection screen, an architectural limit with no fix on that transport (see
// CLAUDE.md "Known Open Issues" -> RUN_REPORT). GetVariants/GetTextElements/
// SetTextElements have no RFC equivalent and stay on the WebSocket, unchanged.

// parseReportParams turns the MCP "params" argument into []saprfc.ReportParam. It
// accepts two shapes: a flat object ({"P_X":"value"}, one EQ parameter per key,
// the pre-existing simple form) or an array of full ReportParam objects (to
// express select-options with a range: {"name":"S_WERKS","option":"BT","low":...,
// "high":...}). An empty/absent string returns (nil, nil).
func parseReportParams(raw string) ([]saprfc.ReportParam, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		var arr []saprfc.ReportParam
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return nil, fmt.Errorf("invalid params array: %w", err)
		}
		return arr, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("invalid params object: %w", err)
	}
	out := make([]saprfc.ReportParam, 0, len(obj))
	for name, v := range obj {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("params.%s must be a string value (use the array form for select-options)", name)
		}
		out = append(out, saprfc.ReportParam{Name: name, Low: s})
	}
	return out, nil
}

// reportWaitSeconds reads the optional wait_seconds argument, defaulting to 30s
// and capping at 120s so a single MCP call cannot block indefinitely.
func reportWaitSeconds(request mcp.CallToolRequest) time.Duration {
	const defaultWait, maxWait = 30.0, 120.0
	wait := defaultWait
	if v, ok := request.GetArguments()["wait_seconds"].(float64); ok && v >= 0 {
		wait = v
	}
	if wait > maxWait {
		wait = maxWait
	}
	return time.Duration(wait * float64(time.Second))
}

func (s *Server) handleRunReport(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// A report is arbitrary ABAP; it can write whatever the selection says
	if refused := s.refuseUnderSafety(adt.OpWorkflow, "RunReport"); refused != nil {
		return refused, nil
	}

	report, _ := request.GetArguments()["report"].(string)
	if report == "" {
		return newToolResultError("report parameter is required"), nil
	}
	paramsStr, _ := request.GetArguments()["params"].(string)
	reportParams, err := parseReportParams(paramsStr)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}

	c, err := s.ensureRFCClient(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect via RFC: %v", err)), nil
	}

	run, err := saprfc.RunReport(ctx, c, report, "", reportParams, reportWaitSeconds(request))
	if err != nil {
		return newToolResultError(fmt.Sprintf("RunReport failed: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Report: %s\n", run.Report)
	fmt.Fprintf(&sb, "Job: %s/%s\n", run.JobName, run.JobCount)
	fmt.Fprintf(&sb, "Status: %s\n\n", run.StatusFor)

	if run.Status == "F" {
		spool, err := saprfc.ReadSpool(ctx, c, run.JobName, run.JobCount)
		if err != nil {
			fmt.Fprintf(&sb, "[spool: error reading - %v]\n", err)
		} else if spool == "" {
			sb.WriteString("No spool output produced.\n")
		} else {
			sb.WriteString("Spool Output:\n")
			sb.WriteString(spool)
		}
	} else {
		sb.WriteString("Job is still running; use GetReportJobStatus to check on it, or raise wait_seconds.\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleRunReportAsync(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Before the task is registered and the goroutine started, not inside it
	if refused := s.refuseUnderSafety(adt.OpWorkflow, "RunReportAsync"); refused != nil {
		return refused, nil
	}

	report, _ := request.GetArguments()["report"].(string)
	if report == "" {
		return newToolResultError("report parameter is required"), nil
	}
	paramsStr, _ := request.GetArguments()["params"].(string)
	reportParams, err := parseReportParams(paramsStr)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}

	// Generate task ID
	s.asyncTasksMu.Lock()
	s.asyncTaskID++
	taskID := fmt.Sprintf("report_%d_%d", time.Now().Unix(), s.asyncTaskID)
	task := &AsyncTask{
		ID:        taskID,
		Type:      "report",
		Status:    "running",
		StartedAt: time.Now(),
	}
	s.asyncTasks[taskID] = task
	s.asyncTasksMu.Unlock()

	go func() {
		bgCtx := context.Background()

		c, err := s.ensureRFCClient(bgCtx)
		if err != nil {
			s.asyncTasksMu.Lock()
			now := time.Now()
			task.EndedAt = &now
			task.Status = "error"
			task.Error = fmt.Sprintf("Failed to connect via RFC: %v", err)
			s.asyncTasksMu.Unlock()
			return
		}

		run, err := saprfc.RunReport(bgCtx, c, report, "", reportParams, 5*time.Minute)
		if err != nil {
			s.asyncTasksMu.Lock()
			now := time.Now()
			task.EndedAt = &now
			task.Status = "error"
			task.Error = fmt.Sprintf("RunReport failed: %v", err)
			s.asyncTasksMu.Unlock()
			return
		}

		var spoolOutput string
		if run.Status == "F" {
			spoolOutput, _ = saprfc.ReadSpool(bgCtx, c, run.JobName, run.JobCount)
		}

		s.asyncTasksMu.Lock()
		now := time.Now()
		task.EndedAt = &now
		task.Status = "completed"
		task.Result = map[string]interface{}{
			"report":       run.Report,
			"jobname":      run.JobName,
			"jobcount":     run.JobCount,
			"job_status":   run.StatusFor,
			"spool_output": spoolOutput,
		}
		s.asyncTasksMu.Unlock()
	}()

	// Return task ID immediately
	output := map[string]string{
		"task_id": taskID,
		"status":  "started",
		"message": "Report execution started in background. Use GetAsyncResult to check status.",
	}
	outputJSON, _ := json.MarshalIndent(output, "", "  ")
	return mcp.NewToolResultText(string(outputJSON)), nil
}

func (s *Server) handleGetReportJobStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	jobName, _ := request.GetArguments()["job_name"].(string)
	jobCount, _ := request.GetArguments()["job_count"].(string)
	if jobName == "" || jobCount == "" {
		return newToolResultError("job_name and job_count parameters are required"), nil
	}

	c, err := s.ensureRFCClient(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect via RFC: %v", err)), nil
	}

	status, statusText, err := saprfc.JobStatus(ctx, c, jobName, jobCount)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetReportJobStatus failed: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Job: %s/%s\n", jobName, jobCount)
	fmt.Fprintf(&sb, "Status: %s\n", statusText)

	includeSpool, _ := request.GetArguments()["include_spool"].(bool)
	if includeSpool && status == "F" {
		spool, err := saprfc.ReadSpool(ctx, c, jobName, jobCount)
		if err != nil {
			fmt.Fprintf(&sb, "[spool: error reading - %v]\n", err)
		} else if spool == "" {
			sb.WriteString("No spool output produced.\n")
		} else {
			sb.WriteString("\nSpool Output:\n")
			sb.WriteString(spool)
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleGetAsyncResult(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, _ := request.GetArguments()["task_id"].(string)
	if taskID == "" {
		return newToolResultError("task_id parameter is required"), nil
	}

	wait, _ := request.GetArguments()["wait"].(bool)

	if wait {
		// Block until complete or timeout
		timeout := time.After(60 * time.Second)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			s.asyncTasksMu.RLock()
			task, exists := s.asyncTasks[taskID]
			if !exists {
				s.asyncTasksMu.RUnlock()
				return newToolResultError(fmt.Sprintf("Task not found: %s", taskID)), nil
			}
			status := task.Status
			s.asyncTasksMu.RUnlock()

			if status != "running" {
				break
			}

			select {
			case <-timeout:
				return newToolResultError("Timeout waiting for task completion"), nil
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return newToolResultError("Request cancelled"), nil
			}
		}
	}

	// Get task status
	s.asyncTasksMu.RLock()
	task, exists := s.asyncTasks[taskID]
	if !exists {
		s.asyncTasksMu.RUnlock()
		return newToolResultError(fmt.Sprintf("Task not found: %s", taskID)), nil
	}
	// Make a copy for safe access
	taskCopy := *task
	s.asyncTasksMu.RUnlock()

	// Format output
	output, _ := json.MarshalIndent(taskCopy, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

func (s *Server) handleGetVariants(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if errResult := s.ensureWSConnected(ctx, "GetVariants"); errResult != nil {
		return errResult, nil
	}

	report, _ := request.GetArguments()["report"].(string)
	if report == "" {
		return newToolResultError("report parameter is required"), nil
	}

	result, err := s.amdpWSClient.GetVariants(ctx, report)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetVariants failed: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Variants for %s:\n\n", result.Report)

	if len(result.Variants) == 0 {
		sb.WriteString("No variants found.\n")
	} else {
		for _, v := range result.Variants {
			if v.Protected {
				fmt.Fprintf(&sb, "  %s (protected)\n", v.Name)
			} else {
				fmt.Fprintf(&sb, "  %s\n", v.Name)
			}
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleGetTextElements(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if errResult := s.ensureWSConnected(ctx, "GetTextElements"); errResult != nil {
		return errResult, nil
	}

	program, _ := request.GetArguments()["program"].(string)
	if program == "" {
		return newToolResultError("program parameter is required"), nil
	}

	language, _ := request.GetArguments()["language"].(string)

	result, err := s.amdpWSClient.GetTextElements(ctx, program, language)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GetTextElements failed: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Text Elements for %s (Language: %s)\n\n", result.Program, result.Language)

	sb.WriteString("Selection Texts:\n")
	if len(result.SelectionTexts) == 0 {
		sb.WriteString("  (none)\n")
	} else {
		for key, text := range result.SelectionTexts {
			fmt.Fprintf(&sb, "  %s: %s\n", key, text)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("Text Symbols:\n")
	if len(result.TextSymbols) == 0 {
		sb.WriteString("  (none)\n")
	} else {
		for key, text := range result.TextSymbols {
			fmt.Fprintf(&sb, "  TEXT-%s: %s\n", key, text)
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleSetTextElements(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if refused := s.refuseUnderSafety(adt.OpUpdate, "SetTextElements"); refused != nil {
		return refused, nil
	}

	if errResult := s.ensureWSConnected(ctx, "SetTextElements"); errResult != nil {
		return errResult, nil
	}

	program, _ := request.GetArguments()["program"].(string)
	if program == "" {
		return newToolResultError("program parameter is required"), nil
	}

	params := adt.SetTextElementsParams{
		Program: program,
	}

	if language, ok := request.GetArguments()["language"].(string); ok {
		params.Language = language
	}

	if selTextsStr, ok := request.GetArguments()["selection_texts"].(string); ok && selTextsStr != "" {
		var selTexts map[string]string
		if err := json.Unmarshal([]byte(selTextsStr), &selTexts); err != nil {
			return newToolResultError(fmt.Sprintf("Invalid selection_texts JSON: %v", err)), nil
		}
		params.SelectionTexts = selTexts
	}

	if textSymsStr, ok := request.GetArguments()["text_symbols"].(string); ok && textSymsStr != "" {
		var textSyms map[string]string
		if err := json.Unmarshal([]byte(textSymsStr), &textSyms); err != nil {
			return newToolResultError(fmt.Sprintf("Invalid text_symbols JSON: %v", err)), nil
		}
		params.TextSymbols = textSyms
	}

	// NOTE: heading_texts is accepted and forwarded to SAP, but the live
	// ZCL_VSP_REPORT_SERVICE=>handle_set_text_elements never reads it — the
	// ABAP READ TEXTPOOL/INSERT TEXTPOOL round-trip only touches id='S'
	// (selection texts) and id='I' (text symbols). result.HeadingTextsSet
	// will always be 0. Verified against the live ABAP source; not fixed
	// per user decision (kept for API completeness).
	if headTextsStr, ok := request.GetArguments()["heading_texts"].(string); ok && headTextsStr != "" {
		var headTexts map[string]string
		if err := json.Unmarshal([]byte(headTextsStr), &headTexts); err != nil {
			return newToolResultError(fmt.Sprintf("Invalid heading_texts JSON: %v", err)), nil
		}
		params.HeadingTexts = headTexts
	}

	if params.SelectionTexts == nil && params.TextSymbols == nil && params.HeadingTexts == nil {
		return newToolResultError("At least one of selection_texts, text_symbols, or heading_texts is required"), nil
	}

	result, err := s.amdpWSClient.SetTextElements(ctx, params)
	if err != nil {
		return newToolResultError(fmt.Sprintf("SetTextElements failed: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Text Elements Updated for %s (Language: %s)\n\n", result.Program, result.Language)
	fmt.Fprintf(&sb, "Status: %s\n", result.Status)
	fmt.Fprintf(&sb, "Selection Texts Set: %d\n", result.SelectionTextsSet)
	fmt.Fprintf(&sb, "Text Symbols Set: %d\n", result.TextSymbolsSet)
	fmt.Fprintf(&sb, "Heading Texts Set: %d\n", result.HeadingTextsSet)

	return mcp.NewToolResultText(sb.String()), nil
}
