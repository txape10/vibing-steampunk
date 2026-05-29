package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/oisee/vibing-steampunk/internal/mcp"
	"github.com/oisee/vibing-steampunk/pkg/graph"
	"github.com/spf13/cobra"
)

var hardcodeUsageCmd = &cobra.Command{
	Use:   "hardcode-usage",
	Short: "Find ZTCA_HARDCODE entries and their callers",
	Long: `Finds ZTCA_HARDCODE configuration entries filtered by FIELD or calling PROGRAM,
then enriches each entry with usage analysis: which programs actually call it,
whether the SUBKEYFLD is correctly configured, and the confidence level.

Entry states:
  OK   — SUBKEYFLD matches exactly one confirmed caller (standard usage)
  INFO — multiple confirmed callers (intentional reuse)
  WARN — SUBKEYFLD does not match any confirmed caller (misconfigured)
  DEAD — no caller found anywhere in the system
  DYN  — callers found but could not be grep-confirmed (dynamic i_subkey)

Examples:
  vsp hardcode-usage --field FRA_ABONO
  vsp hardcode-usage --program ZXEDFU02
  vsp hardcode-usage --field FRA_ABONO --program ZXEDFU02
  vsp hardcode-usage --field FRA_ABONO --format json
  vsp hardcode-usage --field FRA_ABONO --no-grep`,
	RunE: runHardcodeUsage,
}

var hardcodeAuditCmd = &cobra.Command{
	Use:   "hardcode-audit",
	Short: "Full audit of all ZTCA_HARDCODE entries and their system usage",
	Long: `Reads all entries from ZTCA_HARDCODE and cross-references them against
CROSS (direct SELECT) and WBCROSSGT (ZCL_GET_HARDCODE accessor) to determine
which programs actually call each entry. Classifies every entry by status.

Entry states:
  OK   — SUBKEYFLD matches exactly one confirmed caller (standard usage)
  INFO — multiple confirmed callers (intentional reuse)
  WARN — SUBKEYFLD does not match any confirmed caller (misconfigured)
  DEAD — no caller found anywhere in the system
  DYN  — callers found but could not be grep-confirmed (dynamic i_subkey)

Use --format html to generate a standalone catalog for documentation.

Examples:
  vsp hardcode-audit
  vsp hardcode-audit --format json
  vsp hardcode-audit --format html > hardcode-catalog.html
  vsp hardcode-audit --no-grep`,
	RunE: runHardcodeAudit,
}

func init() {
	hardcodeUsageCmd.Flags().String("field", "", "Filter by FIELD column value (e.g. FRA_ABONO)")
	hardcodeUsageCmd.Flags().String("program", "", "Filter by SUBKEYFLD (calling program, e.g. ZXEDFU02)")
	hardcodeUsageCmd.Flags().String("format", "text", "Output format: text, json, html")
	hardcodeUsageCmd.Flags().Bool("no-grep", false, "Skip grep confirmation (faster, MEDIUM confidence only)")
	rootCmd.AddCommand(hardcodeUsageCmd)

	hardcodeAuditCmd.Flags().String("format", "text", "Output format: text, json, html")
	hardcodeAuditCmd.Flags().Bool("no-grep", false, "Skip grep confirmation (faster, MEDIUM confidence only)")
	rootCmd.AddCommand(hardcodeAuditCmd)
}

func runHardcodeUsage(cmd *cobra.Command, _ []string) error {
	params, err := resolveSystemParams(cmd)
	if err != nil {
		return err
	}
	client, err := getClient(params)
	if err != nil {
		return err
	}

	field, _ := cmd.Flags().GetString("field")
	program, _ := cmd.Flags().GetString("program")
	format, _ := cmd.Flags().GetString("format")
	noGrep, _ := cmd.Flags().GetBool("no-grep")

	if field == "" && program == "" {
		return fmt.Errorf("provide --field or --program (or both)")
	}

	ctx := context.Background()
	srv := mcp.NewServerForCLI(client)

	result, err := srv.RunHardcodeUsage(ctx, strings.ToUpper(field), strings.ToUpper(program), !noGrep)
	if err != nil {
		return err
	}

	return printHardcodeUsage(format, result)
}

func runHardcodeAudit(cmd *cobra.Command, _ []string) error {
	params, err := resolveSystemParams(cmd)
	if err != nil {
		return err
	}
	client, err := getClient(params)
	if err != nil {
		return err
	}

	format, _ := cmd.Flags().GetString("format")
	noGrep, _ := cmd.Flags().GetBool("no-grep")

	ctx := context.Background()
	srv := mcp.NewServerForCLI(client)

	result, err := srv.RunHardcodeAudit(ctx, !noGrep)
	if err != nil {
		return err
	}

	return printHardcodeAudit(format, result)
}

// --- Output formatters ---

func printHardcodeUsage(format string, result *graph.HardcodeUsageResult) error {
	switch format {
	case "json":
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	case "html":
		printHardcodeUsageHTML(result)
	default:
		printHardcodeUsageText(result)
	}
	return nil
}

func printHardcodeAudit(format string, result *graph.HardcodeAuditResult) error {
	switch format {
	case "json":
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	case "html":
		printHardcodeAuditHTML(result)
	default:
		printHardcodeAuditText(result)
	}
	return nil
}

func printHardcodeUsageText(r *graph.HardcodeUsageResult) {
	fmt.Printf("ZTCA_HARDCODE usage: %s\n", r.Filter)
	if !r.Found {
		fmt.Println("No entries found.")
		return
	}
	fmt.Printf("%d entries found.\n\n", len(r.Entries))
	printHardcodeEntriesText(r.Entries)
}

func printHardcodeAuditText(r *graph.HardcodeAuditResult) {
	fmt.Printf("ZTCA_HARDCODE — Full Audit\n")
	fmt.Printf("Total: %d  |  OK: %d  INFO: %d  WARN: %d  DEAD: %d  DYN: %d\n\n",
		r.TotalEntries, r.StandardCount, r.ReuseCount, r.MisconfiguredCount, r.DeadCount, r.DynamicCount)
	printHardcodeEntriesText(r.Entries)
}

func printHardcodeEntriesText(entries []graph.HardcodeEntry) {
	graph.SortHardcodeEntries(entries)

	currentArea := ""
	for _, e := range entries {
		if e.Area != currentArea {
			fmt.Printf("── AREA: %s ──\n", e.Area)
			currentArea = e.Area
		}

		statusLabel := graph.StatusEmoji(e.Status)
		callerNames := make([]string, 0, len(e.Callers))
		for _, c := range e.Callers {
			conf := ""
			if c.Confidence == "MEDIUM" {
				conf = "?"
			}
			callerNames = append(callerNames, c.ObjectName+conf)
		}
		callerStr := strings.Join(callerNames, ", ")
		if callerStr == "" {
			callerStr = "(none)"
		}

		fmt.Printf("  [%s] %-30s  SUBKEYFLD=%-20s  callers: %s\n",
			statusLabel, e.Field, e.SubKeyFld, callerStr)

		if e.Status == graph.StatusMisconfigured {
			fmt.Printf("       WARN: SUBKEYFLD=%q not found in callers\n", e.SubKeyFld)
		}
	}
}

// --- HTML output ---

const hardcodeHTMLHeader = `<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="UTF-8">
<title>ZTCA_HARDCODE — %s</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; max-width: 1200px; margin: 2em auto; padding: 0 1em; color: #333; }
  h1 { border-bottom: 2px solid #ddd; padding-bottom: 0.3em; }
  h2 { margin-top: 2em; color: #555; border-bottom: 1px solid #eee; padding-bottom: 0.2em; }
  table { border-collapse: collapse; width: 100%%; margin: 1em 0; font-size: 0.88em; }
  th, td { border: 1px solid #ddd; padding: 5px 8px; text-align: left; }
  th { background: #f5f5f5; font-weight: 600; }
  code { font-family: 'SFMono-Regular', Consolas, monospace; font-size: 0.9em; }
  .status-ok   { color: #2e7d32; font-weight: bold; }
  .status-info { color: #1565c0; font-weight: bold; }
  .status-warn { color: #e65100; font-weight: bold; }
  .status-dead { color: #c62828; font-weight: bold; }
  .status-dyn  { color: #6a1b9a; font-weight: bold; }
  .summary { display: flex; gap: 1em; flex-wrap: wrap; margin: 1em 0; }
  .summary .box { background: #f9f9f9; border: 1px solid #ddd; border-radius: 6px; padding: 0.6em 1em; min-width: 5em; text-align: center; }
  .summary .num { font-size: 1.4em; font-weight: bold; display: block; }
  .summary .label { font-size: 0.8em; color: #666; }
  .conf-high { color: #2e7d32; }
  .conf-med  { color: #e65100; }
  footer { margin-top: 3em; font-size: 0.8em; color: #999; border-top: 1px solid #eee; padding-top: 0.5em; }
</style>
</head><body>
`

func printHardcodeUsageHTML(r *graph.HardcodeUsageResult) {
	esc := html.EscapeString
	title := "Uso de ZTCA_HARDCODE — " + esc(r.Filter)
	fmt.Printf(hardcodeHTMLHeader, title)
	fmt.Printf("<h1>ZTCA_HARDCODE — <code>%s</code></h1>\n", esc(r.Filter))
	if !r.Found {
		fmt.Println("<p>No se encontraron entradas.</p>")
	} else {
		fmt.Printf("<p>%d entradas encontradas.</p>\n", len(r.Entries))
		printHardcodeEntriesHTMLTable(r.Entries)
	}
	fmt.Printf("<footer>Generado: %s</footer></body></html>\n", time.Now().Format("2006-01-02 15:04:05"))
}

func printHardcodeAuditHTML(r *graph.HardcodeAuditResult) {
	esc := html.EscapeString
	_ = esc
	title := "ZTCA_HARDCODE — Auditoría completa"
	fmt.Printf(hardcodeHTMLHeader, title)
	fmt.Println("<h1>ZTCA_HARDCODE — Auditoría completa</h1>")

	fmt.Println(`<div class="summary">`)
	fmt.Printf(`<div class="box"><span class="num">%d</span><span class="label">Total</span></div>`, r.TotalEntries)
	fmt.Printf(`<div class="box"><span class="num status-ok">%d</span><span class="label">OK</span></div>`, r.StandardCount)
	fmt.Printf(`<div class="box"><span class="num status-info">%d</span><span class="label">INFO</span></div>`, r.ReuseCount)
	fmt.Printf(`<div class="box"><span class="num status-warn">%d</span><span class="label">WARN</span></div>`, r.MisconfiguredCount)
	fmt.Printf(`<div class="box"><span class="num status-dead">%d</span><span class="label">DEAD</span></div>`, r.DeadCount)
	fmt.Printf(`<div class="box"><span class="num status-dyn">%d</span><span class="label">DYN</span></div>`, r.DynamicCount)
	fmt.Println(`</div>`)

	printHardcodeEntriesHTMLTable(r.Entries)
	fmt.Printf("<footer>Generado: %s</footer></body></html>\n", time.Now().Format("2006-01-02 15:04:05"))
}

func printHardcodeEntriesHTMLTable(entries []graph.HardcodeEntry) {
	esc := html.EscapeString
	graph.SortHardcodeEntries(entries)

	// Group by Area
	areas := make(map[string][]graph.HardcodeEntry)
	var areaOrder []string
	for _, e := range entries {
		if _, seen := areas[e.Area]; !seen {
			areaOrder = append(areaOrder, e.Area)
		}
		areas[e.Area] = append(areas[e.Area], e)
	}

	for _, area := range areaOrder {
		areaEntries := areas[area]
		fmt.Printf("<h2>AREA: <code>%s</code> (%d entradas)</h2>\n", esc(area), len(areaEntries))
		fmt.Println(`<table>`)
		fmt.Println(`<tr>
  <th>Estado</th>
  <th>FIELD</th>
  <th>KEYFLD</th>
  <th>KEYVAL</th>
  <th>SUBKEYFLD (esperado)</th>
  <th>Callers confirmados</th>
  <th>Nota</th>
</tr>`)

		for _, e := range areaEntries {
			statusClass, statusLabel := hardcodeStatusHTML(e.Status)
			callersHTML := buildCallersHTML(e.Callers)
			note := esc(e.Note)
			if e.Status == graph.StatusMisconfigured {
				if note != "" {
					note += " — "
				}
				note += "SUBKEYFLD no coincide con ningún caller"
			}
			fmt.Printf(`<tr>
  <td class="%s">%s</td>
  <td><code>%s</code></td>
  <td><code>%s</code></td>
  <td><code>%s</code></td>
  <td><code>%s</code></td>
  <td>%s</td>
  <td>%s</td>
</tr>
`, statusClass, statusLabel,
				esc(e.Field), esc(e.KeyFld), esc(e.KeyVal), esc(e.SubKeyFld),
				callersHTML, note)
		}
		fmt.Println(`</table>`)
	}
}

func hardcodeStatusHTML(s graph.HardcodeEntryStatus) (cssClass, label string) {
	switch s {
	case graph.StatusStandard:
		return "status-ok", "✅ OK"
	case graph.StatusReuse:
		return "status-info", "ℹ️ INFO"
	case graph.StatusMisconfigured:
		return "status-warn", "⚠️ WARN"
	case graph.StatusDead:
		return "status-dead", "❌ DEAD"
	case graph.StatusDynamic:
		return "status-dyn", "❓ DYN"
	default:
		return "", string(s)
	}
}

func buildCallersHTML(callers []graph.HardcodeCaller) string {
	esc := html.EscapeString
	if len(callers) == 0 {
		return "<em>ninguno</em>"
	}
	parts := make([]string, 0, len(callers))
	for _, c := range callers {
		confClass := "conf-high"
		confLabel := "HIGH"
		if c.Confidence == "MEDIUM" {
			confClass = "conf-med"
			confLabel = "MED"
		}
		suffix := ""
		if c.CallsDirectSQL && c.CallsAccessor {
			suffix = " (SQL+acc)"
		} else if c.CallsDirectSQL {
			suffix = " (SQL)"
		} else if c.CallsAccessor {
			suffix = " (acc)"
		}
		parts = append(parts, fmt.Sprintf(`<code>%s</code> <span class="%s">[%s]</span>%s`,
			esc(c.ObjectName), confClass, confLabel, esc(suffix)))
	}
	return strings.Join(parts, "<br>")
}

