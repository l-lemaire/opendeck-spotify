// Every Go file starts with a package clause. A directory is one package.
// The special name "main" tells the compiler this package builds into an
// executable, and that execution starts at the function named main.
//
// By convention, executables live under cmd/<binary-name>/. The binary
// produced from this directory is therefore called "spotify-cli".
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
)

// version is a plain package-level variable. The Makefile overwrites it at
// build time with the linker flag -X, so a released binary reports its real
// version while a local "go run" reports "dev".
var version = "dev"

// app carries what every command needs. Commands are methods on it, so they
// reach the logger and context without global variables.
type app struct {
	// log is nil unless --debug was given; the hue package treats nil as
	// "no debug output".
	log *log.Logger
	// ctx is cancelled on Ctrl-C so network operations stop cleanly.
	ctx context.Context
}

// main is the entry point. It takes no arguments and returns nothing.
// Command-line arguments are read from os.Args, and the exit code is set by
// calling os.Exit. Keeping main tiny is a Go habit: it should only wire things
// together and translate a returned error into an exit code, so that the real
// logic lives in functions that can be unit tested.
func main() {
	if err := run(os.Args[1:]); err != nil {
		// flag.ErrHelp is returned when the user asked for -h; usage was
		// already printed, so exit quietly.
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		// Errors go to stderr so that stdout stays clean for real output
		// (which matters with --json for scripting).
		fmt.Fprintln(os.Stderr, "spotify-cli:", err)
		os.Exit(1)
	}
}

// run parses the global flags, picks the subcommand and executes it.
// Returning an error instead of exiting directly is the idiomatic Go way to
// report failure: the caller decides what to do. Go has no exceptions. A
// function that can fail returns an error as its last result, and the caller
// checks it with "if err != nil".
func run(args []string) error {
	// A FlagSet parses flags for one command. The standard library's flag
	// package stops at the first non-flag argument, so global flags must come
	// before the subcommand: `spotify-cli --debug status`, not `spotify-cli status --debug`.
	global := flag.NewFlagSet("spotify-cli", flag.ContinueOnError)
	global.Usage = func() { printUsage(global) }
	debug := global.Bool("debug", false, "print internals to stderr: HTTP requests/responses, discovery steps, credential store access")

	if err := global.Parse(args); err != nil {
		return err
	}
	rest := global.Args() // what is left after the global flags
	if len(rest) == 0 {
		global.Usage()
		return errors.New("a command is required")
	}

	a := &app{ctx: interruptContext()}
	if *debug {
		// Lmicroseconds timestamps let you eyeball latency between steps.
		a.log = log.New(os.Stderr, "debug: ", log.Ltime|log.Lmicroseconds)
	}

	command, commandArgs := rest[0], rest[1:]
	switch command {
	case "status":
		return a.status(commandArgs)
	case "play-pause", "next", "previous":
		return a.control(command, commandArgs)
	case "watch":
		return a.watch(commandArgs)
	case "auth":
		return a.auth(commandArgs)
	case "loop":
		return a.loop(commandArgs)
	case "devices":
		return a.devices(commandArgs)
	case "version":
		if len(commandArgs) > 0 {
			return fmt.Errorf("version: unexpected argument %q", commandArgs[0])
		}
		fmt.Println("spotify-cli", version)
		return nil
	case "help", "-h", "--help":
		global.Usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: spotify-cli help)", command)
	}
}

// parseFlags parses a command's flags and refuses anything left over.
//
// The flag package stops at the first word that is not a flag and leaves it
// in fs.Args() without complaint. Left unchecked, `hue auth version` would
// silently start pairing. Every command goes through this helper so a typo
// or a misplaced word is reported instead of ignored.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %q", fs.Name(), fs.Arg(0))
	}
	return nil
}

func printUsage(fs *flag.FlagSet) {
	fmt.Fprint(fs.Output(), `usage: spotify-cli [global flags] <command> [command flags]

commands:
  status        what is playing: track, artist, position, loop mode
  play-pause    toggle playback
  next          next track
  previous      previous track
  watch         print changes as the local client reports them, until Ctrl-C

  The four above talk to the Spotify desktop client on this machine.
  Add --remote to go through Spotify's web API instead: that controls
  whichever device is playing. --remote needs a login:

  auth          log in to Spotify in the browser (once): auth --client-id <id>
  auth status   show the login state and the active device
  auth forget   remove the stored tokens
  loop off|all|one   set the loop mode (web API only: the client ignores it)
  devices       list your Spotify Connect devices
  version       print the version

global flags (must come before the command):
`)
	fs.PrintDefaults()
}

// interruptContext returns a context that is cancelled when the process gets
// SIGINT (Ctrl-C) or SIGTERM. Passing it to network calls makes them abort
// instead of hanging until their own timeout.
func interruptContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt)
	return ctx
}
