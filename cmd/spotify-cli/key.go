package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"os"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/art"
	"github.com/l-lemaire/opendeck-spotify/internal/mpris"
	"github.com/l-lemaire/opendeck-spotify/internal/player"
	"github.com/l-lemaire/opendeck-spotify/internal/render"
)

// key implements `spotify-cli key --out FILE`: render the play/pause key
// image for the current state, exactly as the plugin would. Handy to check
// display options without a deck.
func (a *app) key(args []string) error {
	fs := flag.NewFlagSet("spotify-cli key", flag.ContinueOnError)
	out := fs.String("out", "key.png", "where to write the PNG")
	noArt := fs.Bool("no-art", false, "do not draw the album cover")
	noTitle := fs.Bool("no-title", false, "do not draw the title")
	noArtist := fs.Bool("no-artist", false, "do not draw the artist")
	noTime := fs.Bool("no-time", false, "do not draw the timing")
	noBar := fs.Bool("no-progress", false, "do not draw the progress bar")
	scale := fs.Float64("text-scale", 1, "text size multiplier")
	backend := fs.String("store", "auto", "credential store for the web API: auto, keyring or file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	p, err := a.buildPlayer(*backend)
	if err != nil {
		return err
	}
	st := p.State()
	info := render.Info{
		Title: st.Track.Title, Artist: st.Track.Artist, Duration: st.Track.Duration,
		Position: st.PositionAt(time.Now()), Playing: st.Playing,
		Remote: st.Source == player.SourceRemote, Unavailable: !st.Available(),
	}
	if !*noArt && st.Track.ArtURL != "" {
		if img, err := art.New(a.log, 1).Get(a.ctx, st.Track.ArtURL); err == nil {
			info.Art = img
		} else {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}
	opts := render.Options{ShowArt: !*noArt, ShowTitle: !*noTitle, ShowArtist: !*noArtist, ShowTime: !*noTime, ShowProgress: !*noBar, TextScale: *scale}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, render.Key(info, opts)); err != nil {
		return err
	}
	fmt.Printf("wrote %s (source: %s)\n", *out, st.Source)
	return nil
}

// buildPlayer assembles the merged player from whatever is available: the
// local client if the bus is reachable, the web API if logged in. It reads
// the state once rather than following it.
func (a *app) buildPlayer(backend string) (*player.Player, error) {
	opts := player.Options{Log: a.log}
	if local, err := mpris.Connect(mpris.SpotifyBusName, a.log); err == nil {
		opts.Local = local
	}
	if remote, err := a.connectRemote(backend); err == nil {
		opts.Remote = remote
	} else if a.log != nil {
		a.log.Printf("player: web API unavailable: %v", err)
	}
	if opts.Local == nil && opts.Remote == nil {
		return nil, errors.New("neither the Spotify client nor the web API is reachable")
	}
	p := player.New(opts)
	// Run until the first state arrives, then stop following.
	ready := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(a.ctx)
	go p.Run(ctx, func(player.State) {
		select {
		case ready <- struct{}{}:
		default:
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
	}
	// A remote-only player reports after its first poll; give it a moment
	// to also see the local client if both exist.
	if opts.Local != nil && opts.Remote != nil {
		time.Sleep(300 * time.Millisecond)
	}
	cancel()
	return p, nil
}
