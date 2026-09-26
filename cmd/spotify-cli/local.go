package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/mpris"
)

// The local commands talk to the Spotify desktop client on this machine
// through MPRIS. They need no account and no network.

// connectLocal opens the session bus and checks the client is running.
func (a *app) connectLocal() (*mpris.Player, error) {
	p, err := mpris.Connect(mpris.SpotifyBusName, a.log)
	if err != nil {
		return nil, err
	}
	running, err := p.Running()
	if err != nil {
		p.Close()
		return nil, err
	}
	if !running {
		p.Close()
		return nil, mpris.ErrNotRunning
	}
	return p, nil
}

// status implements `spotify-cli status`.
func (a *app) status(args []string) error {
	fs := flag.NewFlagSet("spotify-cli status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the state as JSON")
	remote := fs.Bool("remote", false, "ask Spotify's web API instead of the local client (any device)")
	backend := fs.String("store", "auto", "credential store for --remote: auto, keyring or file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *remote {
		return a.remoteStatus(*backend, *asJSON)
	}
	p, err := a.connectLocal()
	if err != nil {
		return err
	}
	defer p.Close()
	st, err := p.State(a.ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(st)
	}
	printState(st)
	return nil
}

// printState renders a snapshot on a few lines.
func printState(st mpris.State) {
	fmt.Printf("%s  %s\n", statusWord(st.Status), st.Track.Title)
	if st.Track.Artist != "" || st.Track.Album != "" {
		fmt.Printf("         %s — %s\n", st.Track.Artist, st.Track.Album)
	}
	fmt.Printf("         %s / %s   loop: %s   shuffle: %v\n", clock(st.Position), clock(st.Track.Length), loopWord(st.Loop), st.Shuffle)
}

// control implements play-pause, next and previous.
func (a *app) control(verb string, args []string) error {
	fs := flag.NewFlagSet("spotify-cli "+verb, flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print what would be sent instead of sending it")
	remote := fs.Bool("remote", false, "send through Spotify's web API (controls whichever device is active)")
	backend := fs.String("store", "auto", "credential store for --remote: auto, keyring or file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *dryRun {
		if *remote {
			fmt.Printf("dry run: would send %s to Spotify's web API\n", verb)
		} else {
			fmt.Printf("dry run: would call %s on %s\n", mprisMethod(verb), mpris.SpotifyBusName)
		}
		return nil
	}
	if *remote {
		return a.remoteControl(verb, *backend)
	}
	p, err := a.connectLocal()
	if err != nil {
		return err
	}
	defer p.Close()
	switch verb {
	case "play-pause":
		err = p.PlayPause(a.ctx)
	case "next":
		err = p.Next(a.ctx)
	case "previous":
		err = p.Previous(a.ctx)
	}
	if err != nil {
		return err
	}
	// The client applies the change asynchronously; a short wait lets us
	// print the resulting state instead of the old one.
	time.Sleep(300 * time.Millisecond)
	st, err := p.State(a.ctx)
	if err != nil {
		return err
	}
	printState(st)
	return nil
}

// watch implements `spotify-cli watch`: print every change until Ctrl-C.
func (a *app) watch(args []string) error {
	fs := flag.NewFlagSet("spotify-cli watch", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print one JSON object per change")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	p, err := mpris.Connect(mpris.SpotifyBusName, a.log)
	if err != nil {
		return err
	}
	defer p.Close()
	fmt.Fprintln(os.Stderr, "Watching the Spotify desktop client; Ctrl-C to stop.")
	return p.Watch(a.ctx, func(e mpris.Event) {
		if *asJSON {
			out, _ := json.Marshal(e)
			fmt.Println(string(out))
			return
		}
		stamp := time.Now().Format("15:04:05")
		if !e.Running {
			fmt.Printf("%s client not running\n", stamp)
			return
		}
		st := e.State
		extra := ""
		if e.Seeked {
			extra = " (seek)"
		}
		fmt.Printf("%s %s %q by %s at %s/%s loop=%s%s\n", stamp, statusWord(st.Status), st.Track.Title, st.Track.Artist,
			clock(st.Position), clock(st.Track.Length), loopWord(st.Loop), extra)
	})
}

func mprisMethod(verb string) string {
	switch verb {
	case "play-pause":
		return "PlayPause"
	case "next":
		return "Next"
	default:
		return "Previous"
	}
}

func statusWord(s string) string {
	switch s {
	case mpris.Playing:
		return "playing"
	case mpris.Paused:
		return "paused "
	case mpris.Stopped:
		return "stopped"
	}
	return s
}

// loopWord uses the words the web API and most players use.
func loopWord(l string) string {
	switch l {
	case mpris.LoopTrack:
		return "one"
	case mpris.LoopPlaylist:
		return "all"
	case mpris.LoopNone, "":
		return "off"
	}
	return l
}

// clock formats a duration as m:ss or h:mm:ss.
func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
