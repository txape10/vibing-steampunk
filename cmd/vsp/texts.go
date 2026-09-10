package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

var textsCmd = &cobra.Command{
	Use:   "texts",
	Short: "A program's or class's text pool: selection texts, text symbols, headings — read and write",
	Long: `The text pool over native ADT REST: selection texts (S) of a program, text
symbols (I) of a program or a class, list headings (H).

  vsp texts get ZDEMO_RUN                             # every entry, with its kind
  vsp texts get CLAS ZCL_DEMO
  vsp texts set ZDEMO_RUN P_DEVC="Package to scan" S_OBJ="Object names"
  vsp texts set ZDEMO_RUN --kind I 001="Nothing found" B01="Options"
  vsp texts set ZDEMO_RUN --dry-run P_DEVC="Package to scan"
  vsp texts set ZDEMO_RUN --lang DE P_DEVC="Zu prüfendes Paket"
  vsp texts set ZDEMO_RUN --delete P_MODE                     # the entry a removed field left behind

A write is a plan first: what is added, what changes from what, what is
already so, and what is refused — a selection text for a field the screen
does not have, a key SAP would reject, a text past 30 characters. Nothing is
locked when nothing differs. The session language is written unless --lang
names another, which is a translation and is refused unless named.`,
}

var textsGetCmd = &cobra.Command{
	Use:   "get [PROG|CLAS] <NAME>",
	Short: "Read the text pool",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, lang, err := textsClient(cmd)
		if err != nil {
			return err
		}
		entries, err := client.TextPool(context.Background(), textTarget(args), lang)
		if err != nil {
			return err
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			return printTextsJSON(entries)
		}
		if len(entries) == 0 {
			fmt.Fprintln(os.Stderr, "no texts")
			return nil
		}
		for _, e := range entries {
			fmt.Printf("%s  %-16s %s\n", e.ID, e.Key, e.Text)
		}
		return nil
	},
}

var textsSetCmd = &cobra.Command{
	Use:   "set [PROG|CLAS] <NAME> KEY=TEXT [KEY=TEXT ...]",
	Short: "Write texts of one kind; keys not named keep theirs",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// The target is the leading argument(s) up to the first KEY=TEXT.
		split := 1
		if len(args) > 2 && !strings.Contains(args[1], "=") {
			split = 2
		}
		target := textTarget(args[:split])
		entries := map[string]string{}
		for _, kv := range args[split:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || strings.TrimSpace(k) == "" {
				return fmt.Errorf("%q: want KEY=TEXT", kv)
			}
			entries[strings.ToUpper(strings.TrimSpace(k))] = v
		}
		del, _ := cmd.Flags().GetStringSlice("delete")
		for _, k := range del {
			entries[strings.ToUpper(strings.TrimSpace(k))] = adt.TextDelete
		}
		if len(entries) == 0 {
			return fmt.Errorf("nothing to write: give KEY=TEXT or --delete KEY")
		}
		client, lang, err := textsClient(cmd)
		if err != nil {
			return err
		}
		kind, _ := cmd.Flags().GetString("kind")
		transport, _ := cmd.Flags().GetString("transport")
		opts := adt.TextPoolOptions{}
		opts.DryRun, _ = cmd.Flags().GetBool("dry-run")
		opts.AllowUnknown, _ = cmd.Flags().GetBool("allow-unknown")
		opts.AnyLanguage = cmd.Flags().Changed("lang")
		plan, err := client.WriteTextPool(context.Background(), target, lang, map[string]map[string]string{strings.ToUpper(kind): entries}, transport, opts)
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON && plan != nil {
			if perr := printTextsJSON(plan); perr != nil {
				return perr
			}
			return err
		}
		if plan != nil {
			printTextPlan(plan)
		}
		return err
	},
}

// textsClient resolves the ADT client and the target language for a texts
// subcommand: --lang if given, else the session language.
func textsClient(cmd *cobra.Command) (*adt.Client, string, error) {
	params, err := resolveSystemParams(cmd)
	if err != nil {
		return nil, "", err
	}
	client, err := getClient(params)
	if err != nil {
		return nil, "", err
	}
	lang, _ := cmd.Flags().GetString("lang")
	if lang == "" {
		lang = params.Language
	}
	return client, lang, nil
}

func textTarget(args []string) adt.TextPoolTarget {
	if len(args) == 2 {
		return adt.TextPoolTarget{Type: args[0], Name: args[1]}
	}
	return adt.TextPoolTarget{Name: args[0]}
}

func printTextsJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func printTextPlan(p *adt.TextPoolPlan) {
	for _, k := range p.Kinds {
		for _, a := range k.Added {
			fmt.Printf("+ %-3s %-16s %s\n", k.Kind, a.Key, a.Text)
		}
		for _, c := range k.Changed {
			fmt.Printf("~ %-3s %-16s %s  (was: %s)\n", k.Kind, c.Key, c.New, c.Old)
		}
		for _, r := range k.Removed {
			fmt.Printf("- %-3s %-16s removed\n", k.Kind, r)
		}
		for _, u := range k.Unchanged {
			fmt.Printf("= %-3s %-16s unchanged\n", k.Kind, u)
		}
		for _, u := range k.Unknown {
			fmt.Printf("? %-3s %-16s not on the screen; --allow-unknown writes it anyway\n", k.Kind, u)
		}
		for _, r := range k.Refused {
			fmt.Printf("! %-3s %-16s %s\n", k.Kind, r.Key, r.Reason)
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintln(os.Stderr, n)
	}
	switch {
	case p.Applied:
		fmt.Fprintf(os.Stderr, "%d text(s) written to %s (%s", p.Written, p.Target, p.Language)
		if p.Transport != "" {
			fmt.Fprintf(os.Stderr, ", transport %s", p.Transport)
		}
		fmt.Fprintln(os.Stderr, ")")
		if p.Activated {
			fmt.Fprintln(os.Stderr, "text elements activated")
		}
	case p.Written == 0:
		fmt.Fprintln(os.Stderr, "nothing written")
	}
}

func init() {
	for _, c := range []*cobra.Command{textsGetCmd, textsSetCmd} {
		c.Flags().String("lang", "", "Language (ISO code); the session language by default. Naming one allows a translation")
		c.Flags().Bool("json", false, "Emit JSON")
	}
	textsSetCmd.Flags().String("kind", "S", "Text kind: S selection texts, I text symbols, H headings")
	textsSetCmd.Flags().String("transport", "", "Transport request; auto-chosen when empty for a transportable object")
	textsSetCmd.Flags().StringSlice("delete", nil, "Keys to remove — entries a field left behind when it went from the screen")
	textsSetCmd.Flags().Bool("dry-run", false, "Show the plan and write nothing")
	textsSetCmd.Flags().Bool("allow-unknown", false, "Write selection texts for keys the screen does not declare")
	textsCmd.AddCommand(textsGetCmd, textsSetCmd)
	rootCmd.AddCommand(textsCmd)
}
