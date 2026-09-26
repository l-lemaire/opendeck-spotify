// opendeck-spotify is the OpenDeck plugin. OpenDeck starts it and talks to it
// over a WebSocket (internal/openaction). It has no terminal: everything it
// has to say goes to its log file, whose path `spotify-cli plugin status`
// prints.
//
// It follows Spotify through the merged player model (internal/player):
// the desktop client on this machine over MPRIS, and Spotify's web API for
// other devices and the loop mode once the user has logged in from the key
// panel or with `spotify-cli auth`.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/l-lemaire/opendeck-spotify/internal/config"
	"github.com/l-lemaire/opendeck-spotify/internal/openaction"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "opendeck-spotify:", err)
		os.Exit(1)
	}
}

func run() error {
	args, err := openaction.ParseArgs(os.Args[1:])
	if err != nil {
		return fmt.Errorf("%w\nusage: opendeck-spotify -port N -pluginUUID ID -registerEvent EVENT -info JSON (OpenDeck passes these)", err)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	info, debug, closeLog, err := openLogs(cfg.PluginDebug)
	if err != nil {
		return err
	}
	defer closeLog()
	info.Printf("opendeck-spotify %s starting: host %s on %s, debug=%v", version, args.Info.Application.Version, args.Info.Application.Platform, cfg.PluginDebug)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := openaction.Connect(ctx, args, debug)
	if err != nil {
		info.Printf("ERROR %v", err)
		return err
	}
	defer conn.Close()
	info.Printf("connected to OpenDeck on port %d", args.Port)

	p := newPlugin(ctx, conn, info, debug)
	err = conn.Run(ctx, p.handlers())
	if err != nil {
		info.Printf("ERROR %v", err)
		return err
	}
	info.Printf("stopped")
	return nil
}

// openLogs opens the log file and returns the info logger (always) and the
// debug logger (nil unless enabled).
func openLogs(debugEnabled bool) (info, debug *log.Logger, closeFn func(), err error) {
	path, err := config.PluginLogPath()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, nil, err
	}
	var w io.Writer = f
	if fileInfo, err := os.Stderr.Stat(); err == nil && fileInfo.Mode()&os.ModeCharDevice != 0 {
		w = io.MultiWriter(f, os.Stderr)
	}
	flags := log.Ldate | log.Ltime | log.Lmicroseconds
	info = log.New(w, "", flags)
	if debugEnabled {
		debug = log.New(w, "debug: ", flags)
	}
	return info, debug, func() { f.Close() }, nil
}
