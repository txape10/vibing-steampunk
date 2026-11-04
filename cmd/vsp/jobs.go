package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Background jobs (SM37): list them with their steps and spools",
	Long: `Background jobs over plain ADT: TBTCO for the job, TBTCP for its steps —
program, variant, user, the spool each step wrote. Only jobs of the logon client.
Without any filter the last seven days are listed.

  vsp jobs list --since 2026-09-01 --status A          # what was cancelled
  vsp jobs list --name 'ZDEMO*' --user TESTUSER
  vsp jobs list --program ZDEMO_NIGHTLY_RUN            # jobs with a step running it`,
}

var jobsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List jobs, latest start first, with steps and spool numbers",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		params, err := resolveSystemParams(cmd)
		if err != nil {
			return err
		}
		client, err := getClient(params)
		if err != nil {
			return err
		}
		f := adt.JobFilter{}
		f.Name, _ = cmd.Flags().GetString("name")
		f.User, _ = cmd.Flags().GetString("user")
		f.Status, _ = cmd.Flags().GetString("status")
		f.Program, _ = cmd.Flags().GetString("program")
		f.Limit, _ = cmd.Flags().GetInt("top")
		if f.From, err = jobDateFlag(cmd, "since"); err != nil {
			return err
		}
		if f.To, err = jobDateFlag(cmd, "until"); err != nil {
			return err
		}
		jobs, err := client.Jobs(context.Background(), f)
		if err != nil {
			return err
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			out, _ := json.MarshalIndent(jobs, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		if len(jobs) == 0 {
			fmt.Fprintln(os.Stderr, "no jobs match")
			return nil
		}
		for _, j := range jobs {
			when := "-"
			if !j.Started.IsZero() {
				when = j.Started.Format("2006-01-02 15:04:05")
			} else if !j.Scheduled.IsZero() {
				when = "sched " + j.Scheduled.Format("2006-01-02 15:04")
			}
			fmt.Printf("%-19s %-10s %-12s %s/%s", when, j.StatusText, j.User, j.Name, j.Count)
			if j.Duration != "" {
				fmt.Printf("  %s", j.Duration)
			}
			if j.Periodic {
				fmt.Print("  periodic")
			}
			fmt.Println()
			for _, s := range j.Steps {
				prog := s.Program
				if prog == "" {
					prog = s.External
				}
				fmt.Printf("    step %d  %s", s.Step, prog)
				if s.Variant != "" {
					fmt.Printf("  variant %s", s.Variant)
				}
				if s.User != "" && s.User != j.User {
					fmt.Printf("  as %s", s.User)
				}
				if s.Spool != 0 {
					fmt.Printf("  spool %d", s.Spool)
				}
				fmt.Println()
			}
		}
		fmt.Fprintf(os.Stderr, "\n%d jobs\n", len(jobs))
		return nil
	},
}

func jobDateFlag(cmd *cobra.Command, name string) (time.Time, error) {
	v, _ := cmd.Flags().GetString(name)
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", v, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("--%s: invalid date %q, use YYYY-MM-DD", name, v)
	}
	return t, nil
}

func init() {
	jobsListCmd.Flags().String("name", "", "Job name, * and + as wildcards")
	jobsListCmd.Flags().String("user", "", "Scheduling user")
	jobsListCmd.Flags().String("status", "", "Status letters: P scheduled, S released, Y ready, R active, F finished, A cancelled")
	jobsListCmd.Flags().String("program", "", "Only jobs with a step running this program")
	jobsListCmd.Flags().String("since", "", "Earliest scheduling date, YYYY-MM-DD")
	jobsListCmd.Flags().String("until", "", "Latest scheduling date, YYYY-MM-DD")
	jobsListCmd.Flags().Int("top", 50, "Maximum jobs (at most 200)")
	jobsListCmd.Flags().Bool("json", false, "Emit JSON")
	jobsCmd.AddCommand(jobsListCmd)
	rootCmd.AddCommand(jobsCmd)
}
