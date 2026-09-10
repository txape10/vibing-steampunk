package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// descriptionTypes are the object types `vsp description` accepts as a
// leading argument. Anything else in arg[0] is taken to be the object name.
var descriptionTypes = map[string]bool{
	"PROG": true, "INCL": true, "CLAS": true, "INTF": true,
	"FUGR": true, "FUNC": true, "TABL": true, "DDLS": true,
}

var descriptionCmd = &cobra.Command{
	Use:   "description [TYPE] NAME [\"new text\"]",
	Short: "Read or change an object's SE80/SE11 short description",
	Long: `The short text SE80/SE11 shows next to an object's name
(adtcore:description) — read it, or change it without rewriting the source.

  vsp description ZDEMO_XFER                         # read (type defaults to PROG)
  vsp description CLAS ZCL_DEMO
  vsp description ZDEMO_XFER "DPL snapshot transfer"  # change it
  vsp description CLAS ZCL_DEMO "Demo class"
  vsp description FUNC Z_TAX --parent Z_FG "Calculates tax"

A change takes and releases its own lock and is a no-op (nothing locked)
when the description already matches. The text is written in the session
language. The transport is auto-chosen for a transportable object unless
--transport names one.`,
	Args: cobra.RangeArgs(1, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		objectType, name, text, write := parseDescriptionArgs(args)

		params, err := resolveSystemParams(cmd)
		if err != nil {
			return err
		}
		client, err := getClient(params)
		if err != nil {
			return err
		}
		parent, _ := cmd.Flags().GetString("parent")
		asJSON, _ := cmd.Flags().GetBool("json")

		if !write {
			res, err := client.GetDescription(context.Background(), objectType, name, parent)
			if err != nil {
				return err
			}
			if asJSON {
				return printTextsJSON(res)
			}
			if res.Old == "" {
				fmt.Fprintln(os.Stderr, "(no description)")
			} else {
				fmt.Println(res.Old)
			}
			if res.Limit > 0 {
				fmt.Fprintf(os.Stderr, "limit: %d characters\n", res.Limit)
			}
			return nil
		}

		transport, _ := cmd.Flags().GetString("transport")
		res, err := client.SetDescription(context.Background(), objectType, name, parent, text, transport)
		if asJSON && res != nil {
			if perr := printTextsJSON(res); perr != nil {
				return perr
			}
			return err
		}
		// Notes (e.g. an activation warning) are worth showing even when the
		// call ends in an error.
		if res != nil {
			for _, n := range res.Notes {
				fmt.Fprintln(os.Stderr, n)
			}
		}
		if err != nil {
			return err
		}
		printDescriptionResult(res)
		return nil
	},
}

// parseDescriptionArgs reads [TYPE] NAME ["text"]. arg[0] is a type only if
// it is one of descriptionTypes; otherwise it is the name.
func parseDescriptionArgs(args []string) (objectType, name, text string, write bool) {
	if len(args) > 0 && descriptionTypes[strings.ToUpper(args[0])] {
		objectType = strings.ToUpper(args[0])
		args = args[1:]
	}
	if len(args) > 0 {
		name = args[0]
	}
	if len(args) > 1 {
		text, write = args[1], true
	}
	return objectType, name, text, write
}

func printDescriptionResult(res *adt.DescriptionResult) {
	if res == nil {
		return
	}
	if !res.Changed {
		fmt.Fprintf(os.Stderr, "unchanged: %q\n", res.Old)
		return
	}
	fmt.Fprintf(os.Stderr, "%q -> %q\n", res.Old, res.New)
	if res.Transport != "" {
		if res.TransportNote != "" {
			fmt.Fprintf(os.Stderr, "transport %s (%s)\n", res.Transport, res.TransportNote)
		} else {
			fmt.Fprintf(os.Stderr, "transport %s\n", res.Transport)
		}
	}
}

func init() {
	descriptionCmd.Flags().String("parent", "", "Function group (required only for FUNC)")
	descriptionCmd.Flags().String("transport", "", "Transport request; auto-chosen when empty for a transportable object")
	descriptionCmd.Flags().Bool("json", false, "Emit JSON")
	rootCmd.AddCommand(descriptionCmd)
}
