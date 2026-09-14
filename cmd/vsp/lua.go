package main

import (
	"fmt"
	"os"

	"github.com/oisee/vibing-steampunk/pkg/scripting"
	"github.com/spf13/cobra"
)

var luaCmd = &cobra.Command{
	Use:   "lua [script.lua]",
	Short: "Run Lua scripts or interactive REPL",
	Long: `Run Lua scripts for automated debugging, testing, and analysis.

Without arguments, starts an interactive Lua REPL.
With a script file, executes the script.

Available modules:
  - All MCP tools (searchObject, getSource, setBreakpoint, etc.)
  - Debug session management (listen, attach, stepOver, etc.)
  - Checkpoints (saveCheckpoint, injectCheckpoint)
  - JSON encoding/decoding
  - print, sleep utilities

Examples:
  # Start interactive REPL
  vsp lua

  # Run a script
  vsp lua scripts/debug-pricing.lua

  # Execute inline script
  vsp lua -e 'print(searchObject("ZCL_*", "CLAS"))'`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLua,
}

var (
	luaExec    string
	luaVerbose bool
)

func init() {
	luaCmd.Flags().StringVarP(&luaExec, "exec", "e", "", "Execute Lua code directly")
	luaCmd.Flags().BoolVarP(&luaVerbose, "verbose", "v", false, "Verbose output")

	rootCmd.AddCommand(luaCmd)
}

func runLua(cmd *cobra.Command, args []string) error {
	client, err := createADTClientFor(cmd)
	if err != nil {
		return err
	}

	// Create Lua engine
	engine := scripting.NewLuaEngine(client)
	defer engine.Close()

	// Set output for verbose mode
	if luaVerbose {
		fmt.Fprintf(os.Stderr, "[LUA] Connected to: %s\n", cfg.BaseURL)
		fmt.Fprintf(os.Stderr, "[LUA] Client: %s, Language: %s\n", cfg.Client, cfg.Language)
	}

	// Execute based on mode
	if luaExec != "" {
		// Execute inline code
		if err := engine.Execute(luaExec); err != nil {
			return fmt.Errorf("lua error: %w", err)
		}
		return nil
	}

	if len(args) > 0 {
		// Execute script file
		scriptFile := args[0]
		if _, err := os.Stat(scriptFile); os.IsNotExist(err) {
			return fmt.Errorf("script file not found: %s", scriptFile)
		}

		if luaVerbose {
			fmt.Fprintf(os.Stderr, "[LUA] Running script: %s\n", scriptFile)
		}

		if err := engine.ExecuteFile(scriptFile); err != nil {
			return fmt.Errorf("script error: %w", err)
		}
		return nil
	}

	// Interactive REPL mode
	engine.REPL()
	return nil
}
