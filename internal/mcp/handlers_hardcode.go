package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/graph"
)

// handleHardcodeUsage finds all ZTCA_HARDCODE entries for a specific FIELD or
// PROGRAM, enriched with usage analysis (who actually calls each entry).
//
// MCP examples:
//
//	SAP(action="analyze", params={"type":"hardcode_usage","field":"FRA_ABONO"})
//	SAP(action="analyze", params={"type":"hardcode_usage","program":"ZXEDFU02"})
//	SAP(action="analyze", params={"type":"hardcode_usage","field":"FRA_ABONO","grep":false})
func (s *Server) handleHardcodeUsage(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	field := strings.ToUpper(strings.TrimSpace(getStringParam(args, "field")))
	program := strings.ToUpper(strings.TrimSpace(getStringParam(args, "program")))
	doGrep := true
	if g, ok := getBoolParam(args, "grep"); ok {
		doGrep = g
	}

	if field == "" && program == "" {
		return newToolResultError("Provide 'field' (FIELD column) or 'program' (SUBKEYFLD / calling program). " +
			"Example: SAP(action=\"analyze\", params={\"type\":\"hardcode_usage\",\"field\":\"FRA_ABONO\"})"), nil
	}
	if s.adtClient == nil {
		return newToolResultError("SAP connection required for hardcode_usage"), nil
	}

	entries, filterLabel, err := s.fetchHardcodeEntries(ctx, field, program)
	if err != nil {
		return newToolResultError(fmt.Sprintf("hardcode_usage failed: %v", err)), nil
	}

	globalCallers, err := s.fetchHardcodeGlobalCallers(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("hardcode_usage: fetching callers: %v", err)), nil
	}

	if err := s.enrichHardcodeEntries(ctx, entries, globalCallers, doGrep); err != nil {
		return newToolResultError(fmt.Sprintf("hardcode_usage: enriching entries: %v", err)), nil
	}

	result := &graph.HardcodeUsageResult{
		Filter:  filterLabel,
		Found:   len(entries) > 0,
		Entries: entries,
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return newToolResultError(fmt.Sprintf("JSON marshal error: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// handleHardcodeAudit performs a full system-wide audit of ZTCA_HARDCODE.
// It reads ALL entries, finds all callers, and classifies each entry.
//
// MCP example:
//
//	SAP(action="analyze", params={"type":"hardcode_audit"})
//	SAP(action="analyze", params={"type":"hardcode_audit","grep":false})
func (s *Server) handleHardcodeAudit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	doGrep := true
	if g, ok := getBoolParam(args, "grep"); ok {
		doGrep = g
	}

	if s.adtClient == nil {
		return newToolResultError("SAP connection required for hardcode_audit"), nil
	}

	entries, _, err := s.fetchHardcodeEntries(ctx, "", "")
	if err != nil {
		return newToolResultError(fmt.Sprintf("hardcode_audit: fetching entries: %v", err)), nil
	}

	globalCallers, err := s.fetchHardcodeGlobalCallers(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("hardcode_audit: fetching callers: %v", err)), nil
	}

	if err := s.enrichHardcodeEntries(ctx, entries, globalCallers, doGrep); err != nil {
		return newToolResultError(fmt.Sprintf("hardcode_audit: enriching entries: %v", err)), nil
	}

	result := buildHardcodeAuditResult(entries)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return newToolResultError(fmt.Sprintf("JSON marshal error: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// --- Data acquisition helpers ---

// hardcodeTableName is the custom hardcode configuration table for this system.
const hardcodeTableName = "ZTCA_HARDCODE"

// hardcodeAccessorClass is the accessor class for the hardcode table.
const hardcodeAccessorClass = "ZCL_GET_HARDCODE"

// checkHardcodeTableExists verifies that ZTCA_HARDCODE exists in this system's
// DDIC. Returns a descriptive error if not found — this tool is customer-specific
// and will not work on systems that do not have this table.
func (s *Server) checkHardcodeTableExists(ctx context.Context) error {
	checkQuery := fmt.Sprintf(
		"SELECT TABNAME FROM DD02L WHERE TABNAME = '%s' AND AS4LOCAL = 'A'",
		hardcodeTableName)
	result, err := s.adtClient.RunQuery(ctx, checkQuery, 1)
	if err != nil {
		return fmt.Errorf("table check failed: %w", err)
	}
	if result == nil || len(result.Rows) == 0 {
		return fmt.Errorf(
			"table %s not found in this system's DDIC — this tool is customer-specific "+
				"and only works on systems where this table exists",
			hardcodeTableName)
	}
	return nil
}

// fetchHardcodeEntries reads ZTCA_HARDCODE entries filtered by field or program.
// If both are empty, reads all entries (full audit).
func (s *Server) fetchHardcodeEntries(ctx context.Context, field, program string) ([]graph.HardcodeEntry, string, error) {
	// Fail fast with a descriptive error if the table is not available.
	if err := s.checkHardcodeTableExists(ctx); err != nil {
		return nil, "", err
	}

	var whereClause string
	var filterLabel string

	switch {
	case field != "" && program != "":
		whereClause = fmt.Sprintf("WHERE FIELD = '%s' AND SUBKEYFLD = '%s'", field, program)
		filterLabel = fmt.Sprintf("field=%s program=%s", field, program)
	case field != "":
		whereClause = fmt.Sprintf("WHERE FIELD = '%s'", field)
		filterLabel = fmt.Sprintf("field=%s", field)
	case program != "":
		whereClause = fmt.Sprintf("WHERE SUBKEYFLD = '%s'", program)
		filterLabel = fmt.Sprintf("program=%s", program)
	default:
		whereClause = ""
		filterLabel = "all"
	}

	query := fmt.Sprintf(
		"SELECT AREA, KEYFLD, KEYVAL, SUBKEYFLD, FIELD FROM %s %s ORDER BY AREA, FIELD, KEYFLD, KEYVAL, SUBKEYFLD",
		hardcodeTableName, whereClause)
	queryResult, err := s.adtClient.RunQuery(ctx, query, 5000)
	if err != nil {
		return nil, filterLabel, fmt.Errorf("%s query: %w", hardcodeTableName, err)
	}

	// Deduplicate: one HardcodeEntry per (AREA, KEYFLD, KEYVAL, SUBKEYFLD, FIELD)
	seen := make(map[string]bool)
	var entries []graph.HardcodeEntry
	if queryResult != nil {
		for _, row := range queryResult.Rows {
			area := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", row["AREA"])))
			keyFld := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", row["KEYFLD"])))
			keyVal := strings.TrimSpace(fmt.Sprintf("%v", row["KEYVAL"]))
			subKeyFld := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", row["SUBKEYFLD"])))
			entryField := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", row["FIELD"])))

			key := area + "|" + keyFld + "|" + keyVal + "|" + subKeyFld + "|" + entryField
			if seen[key] {
				continue
			}
			seen[key] = true

			entries = append(entries, graph.HardcodeEntry{
				Area:      area,
				KeyFld:    keyFld,
				KeyVal:    keyVal,
				SubKeyFld: subKeyFld,
				Field:     entryField,
			})
		}
	}

	return entries, filterLabel, nil
}

// hardcodeCallerKey is the deduplication key for global callers.
type hardcodeCallerKey struct {
	objType string
	objName string
}

// fetchHardcodeGlobalCallers finds all objects that reference ZTCA_HARDCODE,
// either via direct SELECT (CROSS TYPE='DA') or via ZCL_GET_HARDCODE accessor
// (WBCROSSGT OTYPE='CL'). Returns MEDIUM-confidence callers; grep promotion
// happens in enrichHardcodeEntries.
func (s *Server) fetchHardcodeGlobalCallers(ctx context.Context) (map[hardcodeCallerKey]*graph.HardcodeCaller, error) {
	accumulated := make(map[hardcodeCallerKey]*graph.HardcodeCaller)

	// Path A: direct SELECT on ZTCA_HARDCODE (CROSS TYPE='DA')
	crossQuery := fmt.Sprintf("SELECT INCLUDE, TYPE, NAME FROM CROSS WHERE NAME = '%s' AND TYPE = 'DA'", hardcodeTableName)
	crossResult, err := s.adtClient.RunQuery(ctx, crossQuery, 500)
	if err != nil {
		return nil, fmt.Errorf("CROSS query for ZTCA_HARDCODE: %w", err)
	}
	if crossResult != nil {
		for _, row := range crossResult.Rows {
			include := strings.TrimSpace(fmt.Sprintf("%v", row["INCLUDE"]))
			if include == "" {
				continue
			}
			_, objType, objName := graph.NormalizeInclude(include)
			k := hardcodeCallerKey{objType, objName}
			if c, exists := accumulated[k]; !exists {
				accumulated[k] = &graph.HardcodeCaller{
					ObjectType:     objType,
					ObjectName:     objName,
					Confidence:     "MEDIUM",
					CallsDirectSQL: true,
				}
			} else {
				c.CallsDirectSQL = true
			}
		}
	}

	// Path B: accessor class ZCL_GET_HARDCODE (WBCROSSGT OTYPE='CL')
	wbQuery := fmt.Sprintf("SELECT INCLUDE, NAME FROM WBCROSSGT WHERE OTYPE = 'CL' AND NAME = '%s'", hardcodeAccessorClass)
	wbResult, err := s.adtClient.RunQuery(ctx, wbQuery, 500)
	if err != nil {
		return nil, fmt.Errorf("WBCROSSGT query for ZCL_GET_HARDCODE: %w", err)
	}
	if wbResult != nil {
		for _, row := range wbResult.Rows {
			include := strings.TrimSpace(fmt.Sprintf("%v", row["INCLUDE"]))
			if include == "" {
				continue
			}
			_, objType, objName := graph.NormalizeInclude(include)
			k := hardcodeCallerKey{objType, objName}
			if c, exists := accumulated[k]; !exists {
				accumulated[k] = &graph.HardcodeCaller{
					ObjectType:    objType,
					ObjectName:    objName,
					Confidence:    "MEDIUM",
					CallsAccessor: true,
				}
			} else {
				c.CallsAccessor = true
			}
		}
	}

	return accumulated, nil
}

// enrichHardcodeEntries attaches per-entry callers and classifies each entry.
//
// When doGrep is true, for each unique FIELD in the entries we grep each global
// caller's source for that FIELD literal — this gives per-field confirmed callers
// (HIGH confidence) vs unconfirmed ones (MEDIUM). Without grep, all global
// callers are attached to every entry at MEDIUM confidence.
func (s *Server) enrichHardcodeEntries(ctx context.Context, entries []graph.HardcodeEntry, globalCallers map[hardcodeCallerKey]*graph.HardcodeCaller, doGrep bool) error {
	if len(globalCallers) == 0 {
		// No callers at all: all entries are DEAD
		for i := range entries {
			graph.ClassifyEntry(&entries[i])
		}
		return nil
	}

	if !doGrep {
		// Attach all global callers to every entry (MEDIUM confidence)
		callerSlice := make([]graph.HardcodeCaller, 0, len(globalCallers))
		for _, c := range globalCallers {
			callerSlice = append(callerSlice, *c)
		}
		for i := range entries {
			entries[i].Callers = callerSlice
			graph.ClassifyEntry(&entries[i])
		}
		return nil
	}

	// With grep: build per-field confirmed caller sets.
	// Collect unique FIELDs from the entries.
	fields := make(map[string]bool)
	for _, e := range entries {
		if e.Field != "" {
			fields[e.Field] = true
		}
	}

	// For each field, grep every global caller for that literal.
	// fieldCallers[field] = slice of confirmed callers for that field.
	fieldCallers := make(map[string][]graph.HardcodeCaller, len(fields))

	for field := range fields {
		var confirmed []graph.HardcodeCaller
		for k, c := range globalCallers {
			objURL := buildADTObjectURL(k.objType, k.objName)
			if objURL == "" {
				confirmed = append(confirmed, *c)
				continue
			}
			grepResult, err := s.adtClient.GrepObject(ctx, objURL, field, true, 0)
			if err != nil || grepResult == nil || len(grepResult.Matches) == 0 {
				// Not confirmed for this field — skip
				continue
			}
			// Also promote the caller's global confidence to HIGH
			c.Confidence = "HIGH"
			confirmed = append(confirmed, *c)
		}
		fieldCallers[field] = confirmed
	}

	// Assign per-entry callers
	for i := range entries {
		entries[i].Callers = fieldCallers[entries[i].Field]
		graph.ClassifyEntry(&entries[i])
	}

	return nil
}

// buildHardcodeAuditResult aggregates classified entries into an audit summary.
func buildHardcodeAuditResult(entries []graph.HardcodeEntry) *graph.HardcodeAuditResult {
	result := &graph.HardcodeAuditResult{
		TotalEntries: len(entries),
		Entries:      entries,
	}

	for _, e := range entries {
		switch e.Status {
		case graph.StatusStandard:
			result.StandardCount++
		case graph.StatusReuse:
			result.ReuseCount++
		case graph.StatusMisconfigured:
			result.MisconfiguredCount++
		case graph.StatusDead:
			result.DeadCount++
		case graph.StatusDynamic:
			result.DynamicCount++
		}
	}

	return result
}
