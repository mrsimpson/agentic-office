package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// cmdConfigShow prints the effective configuration of the files, the defaults and the
// environment, with the source of each value: office config show [--json]. Flags of a command
// are not part of it; they override what it shows. A key is never shown, only whether the
// variable that agent.wake.key-env names is set.
func cmdConfigShow(e *env, _ *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		start, err := os.Getwd()
		if err != nil {
			return err
		}
		s, files, err := loadConfig(start)
		if err != nil {
			return err
		}
		if v := os.Getenv(EnvSession); v != "" {
			s.apply(layer{src: source{Kind: sourceEnv, From: EnvSession}, values: map[string]any{keySession: v}})
		}
		unused := s.unused()
		keyEnv := s.str(keyKeyEnv)
		keySet := os.Getenv(keyEnv) != ""
		if e.json {
			type entry struct {
				Key    string `json:"key"`
				Value  any    `json:"value"`
				Unused bool   `json:"unused,omitempty"`
				source
			}
			out := struct {
				Files    []configFileInfo `json:"files"`
				Settings []entry          `json:"settings"`
				KeySet   bool             `json:"keySet"`
			}{Files: files, Settings: []entry{}, KeySet: keySet}
			for _, k := range s.keys() {
				out.Settings = append(out.Settings, entry{Key: k, Value: configValue(s[k].Value), Unused: contains(unused, k), source: s[k].Source})
			}
			e.printJSON(out)
			return nil
		}
		for _, f := range files {
			state := "not found"
			if f.Found {
				state = "read"
			}
			fmt.Fprintf(e.stdout, "%s file: %s (%s)\n", f.Kind, f.Path, state)
		}
		fmt.Fprintln(e.stdout)
		w := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
		for _, k := range s.keys() {
			note := s[k].Source.String()
			if contains(unused, k) {
				note += "; not used by the wake strategy " + s.str(keyStrategy)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", k, showValue(s[k].Value), note)
		}
		w.Flush()
		state := "not set"
		if keySet {
			state = "set"
		}
		fmt.Fprintf(e.stdout, "\nthe key variable %s is %s\n", keyEnv, state)
		return nil
	}
}

// showValue formats a value for the text output.
func showValue(v any) string {
	switch x := v.(type) {
	case string:
		return fmt.Sprintf("%q", x)
	case []string:
		q := make([]string, len(x))
		for i, s := range x {
			q[i] = fmt.Sprintf("%q", s)
		}
		return "[" + strings.Join(q, ", ") + "]"
	}
	return fmt.Sprint(configValue(v))
}
