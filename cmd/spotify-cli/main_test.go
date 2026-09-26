package main

import (
	"strings"
	"testing"
)

// These tests feed argument lists to run() and check that anything
// malformed is rejected before any network or keyring activity. None of the
// rejected cases below should take longer than a few milliseconds; a hang
// would mean a command started for real.
func TestRunRejectsMalformedArguments(t *testing.T) {
	cases := []struct {
		args []string
		want string // substring expected in the error
	}{
		{[]string{}, "a command is required"},
		{[]string{"nope"}, `unknown command "nope"`},
		{[]string{"version", "extra"}, `unexpected argument "extra"`},
		{[]string{"status", "extra"}, `unexpected argument "extra"`},
		{[]string{"status", "--bogus"}, "flag provided but not defined"},
		{[]string{"play-pause", "now"}, `unexpected argument "now"`},
		{[]string{"next", "--dry-run", "x"}, `unexpected argument "x"`},
		{[]string{"watch", "extra"}, `unexpected argument "extra"`},
		{[]string{"auth", "login"}, `unknown subcommand "login"`},
		{[]string{"auth", "--client-id"}, "flag needs an argument"},
		{[]string{"auth", "status", "extra"}, `unexpected argument "extra"`},
		{[]string{"loop"}, "missing mode"},
		{[]string{"loop", "sometimes"}, "want off, all or one"},
		{[]string{"loop", "all", "extra"}, `unexpected argument "extra"`},
		{[]string{"loop", "all", "--device", "x"}, "flags go before the argument"},
		{[]string{"devices", "extra"}, `unexpected argument "extra"`},
		{[]string{"key", "extra"}, `unexpected argument "extra"`},
		{[]string{"key", "--text-scale", "big"}, "invalid value"},
		{[]string{"plugin"}, "subcommand is required"},
		{[]string{"plugin", "debug", "maybe"}, "want on or off"},
		// global flags must precede the command
		{[]string{"status", "--debug"}, "flag provided but not defined"},
	}
	for _, c := range cases {
		err := run(c.args)
		if err == nil {
			t.Errorf("run(%q): expected an error", c.args)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("run(%q) = %q, want it to mention %q", c.args, err, c.want)
		}
	}
}

func TestRunVersion(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Errorf("version: %v", err)
	}
	if err := run([]string{"--debug", "version"}); err != nil {
		t.Errorf("--debug version: %v", err)
	}
}
