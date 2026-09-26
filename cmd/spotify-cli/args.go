package main

import (
	"encoding/json"
	"flag"
	"fmt"
)

// parseArgs parses flags and requires exactly `want` positional words after
// them. Flags must come before the positional words.
func parseArgs(fs *flag.FlagSet, args []string, want int, names ...string) ([]string, error) {
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	rest := fs.Args()
	switch {
	case len(rest) < want:
		return nil, fmt.Errorf("%s: missing %s", fs.Name(), names[len(rest)])
	case len(rest) > want:
		extra := rest[want]
		hint := ""
		if len(extra) > 0 && extra[0] == '-' {
			hint = " (flags go before the argument)"
		}
		return nil, fmt.Errorf("%s: unexpected argument %q%s", fs.Name(), extra, hint)
	}
	return rest, nil
}

func printJSON(v any) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
