package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/config"
	"github.com/l-lemaire/opendeck-spotify/internal/secrets"
	"github.com/l-lemaire/opendeck-spotify/internal/spotifyapi"
)

// The remote commands use Spotify's web API: they work for whatever device
// is playing, and they are the only way to change the loop mode.

// openStore opens the credential store with the file fallback next to the
// config file, warning when the weaker backend ends up in use unasked.
func (a *app) openStore(backend string) (secrets.Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	store, err := secrets.Open(backend, filepath.Join(dir, "credentials.json"), a.log)
	if err != nil {
		return nil, err
	}
	if backend != secrets.BackendFile && strings.HasPrefix(store.Name(), "file") {
		fmt.Fprintln(os.Stderr, "warning: no desktop keyring reachable, storing tokens in", store.Name())
	}
	return store, nil
}

// connectRemote builds an API client from the config and the stored tokens.
func (a *app) connectRemote(backend string) (*spotifyapi.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg.ClientID == "" {
		return nil, errors.New("no Spotify client id configured; run `spotify-cli auth --client-id <id>` first")
	}
	store, err := a.openStore(backend)
	if err != nil {
		return nil, err
	}
	c, err := spotifyapi.NewClient(spotifyapi.ClientOptions{ClientID: cfg.ClientID, Store: store, Log: a.log})
	if errors.Is(err, spotifyapi.ErrNotLoggedIn) {
		return nil, fmt.Errorf("%w; run `spotify-cli auth`", err)
	}
	return c, err
}

// auth dispatches `spotify-cli auth`, `auth status`, `auth forget`.
func (a *app) auth(args []string) error {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "status":
			return a.authStatus(args[1:])
		case "forget":
			return a.authForget(args[1:])
		default:
			return fmt.Errorf("auth: unknown subcommand %q (want: status, forget, or flags for logging in)", args[0])
		}
	}
	return a.authLogin(args)
}

// authLogin implements `spotify-cli auth`: the browser login.
func (a *app) authLogin(args []string) error {
	fs := flag.NewFlagSet("spotify-cli auth", flag.ContinueOnError)
	clientID := fs.String("client-id", "", "Spotify developer app client id (saved in the config for next time)")
	port := fs.Int("port", spotifyapi.DefaultPort, "callback port; the app's redirect URI must be http://127.0.0.1:<port>/callback")
	noBrowser := fs.Bool("no-browser", false, "print the login URL instead of opening the browser")
	backend := fs.String("store", secrets.BackendAuto, "where to keep the tokens: auto, keyring or file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *clientID != "" {
		cfg.ClientID = *clientID
		if err := cfg.Save(); err != nil {
			return err
		}
	}
	if cfg.ClientID == "" {
		return errors.New("a client id is required the first time: spotify-cli auth --client-id <id>")
	}
	store, err := a.openStore(*backend)
	if err != nil {
		return err
	}

	opts := spotifyapi.LoginOptions{ClientID: cfg.ClientID, Port: *port, Log: a.log}
	if *noBrowser {
		opts.OpenBrowser = func(u string) error {
			fmt.Printf("Open this URL in a browser on this machine:\n\n%s\n\n", u)
			return nil
		}
	} else {
		fmt.Println("Opening Spotify's login page in your browser...")
	}
	fmt.Printf("Waiting for the login to come back on http://127.0.0.1:%d/callback (up to 5 minutes).\n", *port)
	tokens, err := spotifyapi.Login(a.ctx, opts)
	if err != nil {
		return err
	}
	if err := spotifyapi.SaveTokens(store, tokens); err != nil {
		return err
	}
	fmt.Printf("Logged in. Tokens saved in the %s; access token valid until %s and renewed automatically.\n",
		store.Name(), tokens.Expiry.Format(time.Kitchen))
	return nil
}

// authStatus implements `spotify-cli auth status`.
func (a *app) authStatus(args []string) error {
	fs := flag.NewFlagSet("spotify-cli auth status", flag.ContinueOnError)
	backend := fs.String("store", secrets.BackendAuto, "credential store to read: auto, keyring or file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Println("Config file:  ", cfg.Path())
	if cfg.ClientID == "" {
		fmt.Println("Client id:     not set (spotify-cli auth --client-id <id>)")
	} else {
		fmt.Println("Client id:    ", cfg.ClientID)
	}
	store, err := a.openStore(*backend)
	if err != nil {
		return err
	}
	fmt.Println("Token store:  ", store.Name())
	tokens, err := spotifyapi.LoadTokens(store)
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		fmt.Println("Login:         not logged in (spotify-cli auth)")
		return nil
	case err != nil:
		return err
	}
	fmt.Printf("Login:         tokens present, access token %s\n", expiryWord(tokens))
	c, err := a.connectRemote(*backend)
	if err != nil {
		return err
	}
	st, err := c.PlaybackState(a.ctx)
	if err != nil {
		fmt.Println("API check:     failed:", err)
		return nil
	}
	if !st.Active {
		fmt.Println("API check:     ok, nothing playing on any device")
		return nil
	}
	fmt.Printf("API check:     ok, %s on %s (%s)\n", playingWord(st.Playing), st.Device.Name, st.Device.Type)
	return nil
}

// authForget implements `spotify-cli auth forget`: remove the tokens.
// Spotify has no API to revoke them; the user can remove the app from
// spotify.com/account/apps.
func (a *app) authForget(args []string) error {
	fs := flag.NewFlagSet("spotify-cli auth forget", flag.ContinueOnError)
	backend := fs.String("store", secrets.BackendAuto, "credential store: auto, keyring or file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	store, err := a.openStore(*backend)
	if err != nil {
		return err
	}
	if err := store.Delete(spotifyapi.TokensKey); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}
	fmt.Println("Tokens removed. To revoke the app's access entirely, remove it at https://www.spotify.com/account/apps/")
	return nil
}

// remoteStatus prints the web API's view of playback.
func (a *app) remoteStatus(backend string, asJSON bool) error {
	c, err := a.connectRemote(backend)
	if err != nil {
		return err
	}
	st, err := c.PlaybackState(a.ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(st)
	}
	if !st.Active {
		fmt.Println("nothing playing on any device")
		return nil
	}
	fmt.Printf("%s  %s\n", playingWord(st.Playing), st.Track.Title)
	fmt.Printf("         %s — %s\n", st.Track.Artist, st.Track.Album)
	fmt.Printf("         %s / %s   loop: %s   shuffle: %v\n", clock(st.Progress), clock(st.Track.Duration), repeatWord(st.Repeat), st.Shuffle)
	fmt.Printf("         on %s (%s)\n", st.Device.Name, strings.ToLower(st.Device.Type))
	return nil
}

// remoteControl runs a transport command through the API.
func (a *app) remoteControl(verb, backend string) error {
	c, err := a.connectRemote(backend)
	if err != nil {
		return err
	}
	switch verb {
	case "play-pause":
		playing, err := c.PlayPause(a.ctx)
		if err != nil {
			return err
		}
		fmt.Println(playingWord(playing))
		return nil
	case "next":
		err = c.Next(a.ctx, "")
	case "previous":
		err = c.Previous(a.ctx, "")
	}
	if err != nil {
		return err
	}
	fmt.Println(verb, "sent")
	return nil
}

// loop implements `spotify-cli loop off|all|one`, always through the API.
func (a *app) loop(args []string) error {
	fs := flag.NewFlagSet("spotify-cli loop", flag.ContinueOnError)
	backend := fs.String("store", secrets.BackendAuto, "credential store: auto, keyring or file")
	pos, err := parseArgs(fs, args, 1, "mode (off, all or one)")
	if err != nil {
		return err
	}
	var mode string
	switch pos[0] {
	case "off":
		mode = spotifyapi.RepeatOff
	case "all":
		mode = spotifyapi.RepeatContext
	case "one":
		mode = spotifyapi.RepeatTrack
	default:
		return fmt.Errorf("loop: want off, all or one, got %q", pos[0])
	}
	c, err := a.connectRemote(*backend)
	if err != nil {
		return err
	}
	if err := c.SetRepeat(a.ctx, mode, ""); err != nil {
		return err
	}
	fmt.Println("loop:", pos[0])
	return nil
}

// devices implements `spotify-cli devices`.
func (a *app) devices(args []string) error {
	fs := flag.NewFlagSet("spotify-cli devices", flag.ContinueOnError)
	backend := fs.String("store", secrets.BackendAuto, "credential store: auto, keyring or file")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	c, err := a.connectRemote(*backend)
	if err != nil {
		return err
	}
	devices, err := c.Devices(a.ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(devices)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tACTIVE\tVOLUME\tID")
	for _, d := range devices {
		active := ""
		if d.IsActive {
			active = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d%%\t%s\n", d.Name, strings.ToLower(d.Type), active, d.Volume, d.ID)
	}
	return w.Flush()
}

func expiryWord(t spotifyapi.Tokens) string {
	if t.Expired() {
		return "expired (renewed on next use)"
	}
	return "valid for " + time.Until(t.Expiry).Round(time.Minute).String()
}

func playingWord(playing bool) string {
	if playing {
		return "playing"
	}
	return "paused "
}

// repeatWord maps the API's names to the words the CLI uses.
func repeatWord(r string) string {
	switch r {
	case spotifyapi.RepeatTrack:
		return "one"
	case spotifyapi.RepeatContext:
		return "all"
	}
	return "off"
}
