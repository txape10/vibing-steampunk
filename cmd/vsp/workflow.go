package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/dsl"
	"github.com/spf13/cobra"
)

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Run YAML-defined workflows",
	Long: `Execute automation workflows defined in YAML files.

Workflows can include search, test, transform, and other operations.
Use --dry-run to preview changes without executing them.

Example workflow file:
  name: test-package
  steps:
    - action: search
      parameters:
        query: "ZCL_*"
        package: "$TMP"
      saveAs: classes
    - action: test
      parameters:
        objects: classes
      saveAs: results`,
}

var workflowRunCmd = &cobra.Command{
	Use:   "run <workflow.yaml>",
	Short: "Run a workflow from a YAML file",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflow,
}

var workflowTestCmd = &cobra.Command{
	Use:   "test <package-pattern>",
	Short: "Run unit tests for a package pattern",
	Long: `Run ABAP Unit tests for all classes/programs matching a package pattern.

Examples:
  vsp workflow test "$TMP"
  vsp workflow test "$ZRAY*"
  vsp workflow test "ZCL_*" --parallel 4`,
	Args: cobra.ExactArgs(1),
	RunE: runTestWorkflow,
}

var (
	workflowDryRun  bool
	workflowVerbose bool
	workflowVars    map[string]string
	testParallel    int
	testDangerous   bool
	testLong        bool
	testStopOnFail  bool
	outputJSON      bool
)

func init() {
	// Workflow run flags
	workflowRunCmd.Flags().BoolVar(&workflowDryRun, "dry-run", false, "Preview changes without executing")
	workflowRunCmd.Flags().BoolVarP(&workflowVerbose, "verbose", "v", false, "Verbose output")
	workflowRunCmd.Flags().StringToStringVar(&workflowVars, "var", nil, "Set workflow variables (key=value)")

	// Test workflow flags
	workflowTestCmd.Flags().IntVar(&testParallel, "parallel", 1, "Number of parallel test executions")
	workflowTestCmd.Flags().BoolVar(&testDangerous, "dangerous", false, "Include dangerous risk level tests")
	workflowTestCmd.Flags().BoolVar(&testLong, "long", false, "Include long duration tests")
	workflowTestCmd.Flags().BoolVar(&testStopOnFail, "stop-on-fail", false, "Stop on first failure")
	workflowTestCmd.Flags().BoolVar(&outputJSON, "json", false, "Output results as JSON")

	workflowCmd.AddCommand(workflowRunCmd)
	workflowCmd.AddCommand(workflowTestCmd)
	rootCmd.AddCommand(workflowCmd)
}

func runWorkflow(cmd *cobra.Command, args []string) error {
	workflowFile := args[0]

	// Process cookie auth (kept: some workflow steps read cfg.Cookies directly)
	if err := processCookieAuth(cmd); err != nil {
		return err
	}

	client, err := createADTClientFor(cmd)
	if err != nil {
		return err
	}

	// Create workflow engine
	engine := dsl.NewWorkflowEngine(client)

	// Load workflow
	workflow, err := engine.LoadWorkflow(workflowFile)
	if err != nil {
		return fmt.Errorf("failed to load workflow: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Running workflow: %s\n", workflow.Name)
	if workflow.Description != "" {
		fmt.Fprintf(os.Stderr, "Description: %s\n", workflow.Description)
	}
	fmt.Fprintf(os.Stderr, "\n")

	// Build execution options
	opts := []dsl.ExecuteOption{
		dsl.WithDryRun(workflowDryRun),
		dsl.WithVerbose(workflowVerbose),
	}

	if len(workflowVars) > 0 {
		opts = append(opts, dsl.WithVariables(workflowVars))
	}

	// Execute
	ctx := context.Background()
	result, err := engine.Execute(ctx, workflow, opts...)
	if err != nil {
		return fmt.Errorf("workflow execution failed: %w", err)
	}

	// Print results
	printWorkflowResult(result)

	if !result.Success {
		return fmt.Errorf("workflow failed: %s", result.Error)
	}

	return nil
}

func runTestWorkflow(cmd *cobra.Command, args []string) error {
	packagePattern := args[0]

	// Process cookie auth (kept: some workflow steps read cfg.Cookies directly)
	if err := processCookieAuth(cmd); err != nil {
		return err
	}

	client, err := createADTClientFor(cmd)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Discovering tests in: %s\n", packagePattern)

	// Search for testable objects
	ctx := context.Background()
	objects, err := dsl.Search(client).
		Query(packagePattern).
		Types(dsl.TypeClass, dsl.TypeProgram).
		MaxResults(500).
		Execute(ctx)

	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(objects) == 0 {
		fmt.Println("No testable objects found")
		return nil
	}

	fmt.Fprintf(os.Stderr, "Found %d objects to test\n\n", len(objects))

	// Build test runner
	runner := dsl.Test(client).
		Objects(objects...).
		Parallel(testParallel)

	if testDangerous {
		runner.IncludeDangerous()
	}
	if testLong {
		runner.IncludeLong()
	}
	if testStopOnFail {
		runner.StopOnFirstFailure()
	}

	// Add progress callbacks
	runner.OnStart(func(obj dsl.ObjectRef) {
		if !outputJSON {
			fmt.Fprintf(os.Stderr, "Testing: %s...\n", obj.Name)
		}
	})

	runner.OnComplete(func(obj dsl.ObjectRef, result dsl.TestResult) {
		if !outputJSON {
			status := "PASS"
			if !result.Success {
				status = "FAIL"
			}
			fmt.Fprintf(os.Stderr, "  %s: %s (%d tests, %v)\n",
				status, obj.Name, result.TotalTests, result.ExecutionTime.Round(time.Millisecond))
		}
	})

	// Run tests
	summary, err := runner.Run(ctx)
	if err != nil {
		return fmt.Errorf("test execution failed: %w", err)
	}

	// Output results
	if outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(summary)
	} else {
		fmt.Fprintf(os.Stderr, "\n")
		printTestSummary(summary)
	}

	if summary.FailedTests > 0 {
		return fmt.Errorf("%d tests failed", summary.FailedTests)
	}

	return nil
}

// backfillGlobalConfig copies a resolved system's connection details into the
// global cfg struct, for the handful of call sites (debugSession.printInfo,
// lua.go's verbose banner, lsp.go) that still read cfg.* directly instead of
// taking a systemParams.
func backfillGlobalConfig(params *systemParams) {
	cfg.BaseURL = params.URL
	cfg.Username = params.User
	cfg.Password = params.Password
	if params.Client != "" {
		cfg.Client = params.Client
	}
	if params.Language != "" {
		cfg.Language = params.Language
	}
	cfg.InsecureSkipVerify = cfg.InsecureSkipVerify || params.Insecure
}

// createADTClientFor resolves system parameters for cmd (--system flag,
// .vsp.json default, or bare SAP_* env vars) and builds a client from them —
// the -s-aware replacement for createADTClient, which only ever reads the
// global cfg (never populated by -s on this code path).
func createADTClientFor(cmd *cobra.Command) (*adt.Client, error) {
	params, err := resolveSystemParams(cmd)
	if err != nil {
		return nil, err
	}
	backfillGlobalConfig(params)
	return getClient(params)
}

// Deprecated: use createADTClientFor, which resolves -s/.vsp.json system
// parameters correctly. Kept for the LSP path (lsp.go), which this port
// deliberately leaves untouched — see CLAUDE.md.
func createADTClient() *adt.Client {
	opts := []adt.Option{
		adt.WithClient(cfg.Client),
		adt.WithLanguage(cfg.Language),
	}

	if cfg.InsecureSkipVerify {
		opts = append(opts, adt.WithInsecureSkipVerify())
	}

	if len(cfg.Cookies) > 0 {
		opts = append(opts, adt.WithCookies(cfg.Cookies))
	}

	return adt.NewClient(cfg.BaseURL, cfg.Username, cfg.Password, opts...)
}

func printWorkflowResult(result *dsl.WorkflowResult) {
	fmt.Printf("Workflow: %s\n", result.Name)
	fmt.Printf("Status: %s\n", statusString(result.Success))
	fmt.Printf("Steps: %d\n\n", len(result.StepResults))

	for _, step := range result.StepResults {
		status := "PASS"
		if !step.Success {
			status = "FAIL"
		}
		if step.Skipped {
			status = "SKIP"
		}

		fmt.Printf("  [%s] %s (%s)\n", status, step.Name, step.Action)
		if step.Error != "" {
			fmt.Printf("         Error: %s\n", step.Error)
		}
		if step.SkipReason != "" {
			fmt.Printf("         Reason: %s\n", step.SkipReason)
		}
	}

	if result.Error != "" {
		fmt.Printf("\nError: %s\n", result.Error)
	}
}

func printTestSummary(summary *dsl.TestSummary) {
	fmt.Println("=== Test Summary ===")
	fmt.Printf("Objects: %d tested, %d passed, %d failed\n",
		summary.TestedObjects, summary.PassedObjects, summary.FailedObjects)
	fmt.Printf("Tests:   %d total, %d passed, %d failed\n",
		summary.TotalTests, summary.PassedTests, summary.FailedTests)
	fmt.Printf("Time:    %v\n", summary.TotalTime.Round(time.Millisecond))

	if summary.FailedTests > 0 {
		fmt.Println("\nFailed tests:")
		for _, result := range summary.Results {
			if !result.Success {
				fmt.Printf("  - %s: %s\n", result.Object.Name, result.Error)
				for _, class := range result.Classes {
					for _, method := range class.Methods {
						if !method.Success {
							fmt.Printf("      %s.%s: %s\n", class.Name, method.Name, method.Message)
						}
					}
				}
			}
		}
	}
}

func statusString(success bool) string {
	if success {
		return "SUCCESS"
	}
	return "FAILED"
}
