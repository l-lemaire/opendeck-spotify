package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/art"
	"github.com/l-lemaire/opendeck-spotify/internal/config"
	"github.com/l-lemaire/opendeck-spotify/internal/mpris"
	"github.com/l-lemaire/opendeck-spotify/internal/openaction"
	"github.com/l-lemaire/opendeck-spotify/internal/player"
	"github.com/l-lemaire/opendeck-spotify/internal/render"
	"github.com/l-lemaire/opendeck-spotify/internal/secrets"
	"github.com/l-lemaire/opendeck-spotify/internal/spotifyapi"
)

// plugin holds the merged player and the keys on the deck.
type plugin struct {
	ctx      context.Context
	conn     *openaction.Conn
	info     *log.Logger
	debug    *log.Logger
	art      *art.Cache
	inflight inflightSet

	// sources is how the player is (re)built: nil in production means
	// "connect to the real client and, if logged in, the real API"; tests
	// inject fakes.
	sources func() player.Options

	mu       sync.Mutex
	player   *player.Player
	opts     player.Options     // the sources the current player uses
	cancel   context.CancelFunc // stops the current player.Run
	state    player.State
	buttons  map[string]button // by context
	loggedIn bool
	// inspector is the key whose panel is open, if any, so status updates
	// can be pushed to it as things change.
	inspector *openaction.Event
	// ownsLocal is true when sources opens a new bus connection per call
	// (production), so the previous one must be closed on restart. Tests
	// share one connection and leave it false.
	ownsLocal bool
}

type button struct {
	action   string
	settings Settings
}

func newPlugin(ctx context.Context, conn *openaction.Conn, info, debug *log.Logger) *plugin {
	p := &plugin{ctx: ctx, conn: conn, info: info, debug: debug, art: art.New(debug, 8), buttons: map[string]button{}, ownsLocal: true}
	p.sources = p.realSources
	p.start()
	return p
}

// newPluginWith is newPlugin with injected sources, for tests.
func newPluginWith(ctx context.Context, conn *openaction.Conn, info, debug *log.Logger, sources func() player.Options) *plugin {
	p := &plugin{ctx: ctx, conn: conn, info: info, debug: debug, art: art.New(debug, 8), buttons: map[string]button{}, sources: sources}
	p.start()
	return p
}

func (p *plugin) start() {
	p.startPlayer()
	go p.tick()
}

// realSources connects to the desktop client and, when a login exists, to
// the web API.
func (p *plugin) realSources() player.Options {
	o := player.Options{Log: p.debug}
	if local, err := mpris.Connect(mpris.SpotifyBusName, p.debug); err == nil {
		o.Local = local
	} else {
		p.info.Printf("session bus unavailable: %v (local control disabled)", err)
	}
	cfg, err := config.Load()
	if err != nil {
		p.info.Printf("config: %v", err)
		return o
	}
	if cfg.ClientID == "" {
		return o
	}
	store, err := p.store()
	if err != nil {
		p.info.Printf("credential store: %v", err)
		return o
	}
	remote, err := spotifyapi.NewClient(spotifyapi.ClientOptions{ClientID: cfg.ClientID, Store: store, Log: p.debug})
	switch {
	case errors.Is(err, spotifyapi.ErrNotLoggedIn):
		p.info.Printf("web API: not logged in (connect from a key's panel or with `spotify-cli auth`)")
	case err != nil:
		p.info.Printf("web API: %v", err)
	default:
		o.Remote = remote
	}
	return o
}

func (p *plugin) store() (secrets.Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return secrets.Open(secrets.BackendAuto, filepath.Join(dir, "credentials.json"), p.debug)
}

// startPlayer (re)builds the player from the sources and follows it. Called
// at start and after a login, so the web API joins without a restart.
func (p *plugin) startPlayer() {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	if p.opts.Local != nil && p.ownsLocal {
		p.opts.Local.Close() // the real sources open a fresh connection each time
	}
	opts := p.sources()
	pl := player.New(opts)
	ctx, cancel := context.WithCancel(p.ctx)
	p.player, p.opts, p.cancel, p.loggedIn = pl, opts, cancel, opts.Remote != nil
	p.mu.Unlock()

	p.info.Printf("player started: local=%v webAPI=%v", opts.Local != nil, opts.Remote != nil)
	go pl.Run(ctx, p.onStateChange)
}

// State returns the latest merged state.
func (p *plugin) State() player.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// onStateChange redraws every key when the merged state changes.
func (p *plugin) onStateChange(st player.State) {
	p.mu.Lock()
	p.state = st
	p.mu.Unlock()
	p.info.Printf("state: source=%s playing=%v loop=%s %q by %s", st.Source, st.Playing, st.Loop, st.Track.Title, st.Track.Artist)
	p.redrawAll()
	p.pushStatus()
}

// pushStatus refreshes the open panel, if any.
func (p *plugin) pushStatus() {
	p.mu.Lock()
	ev := p.inspector
	p.mu.Unlock()
	if ev != nil {
		p.sendStatus(*ev)
	}
}

// tick redraws the play/pause keys once a second while music plays and
// some key shows the time or the progress bar.
func (p *plugin) tick() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-t.C:
		}
		p.mu.Lock()
		playing := p.state.Playing
		var due []string
		for ctxID, b := range p.buttons {
			if b.action == actionPlayPause && b.settings.needsTicker() {
				due = append(due, ctxID)
			}
		}
		p.mu.Unlock()
		if !playing {
			continue
		}
		for _, ctxID := range due {
			p.redraw(ctxID)
		}
	}
}

// redrawAll updates every key on the deck for the current state.
func (p *plugin) redrawAll() {
	p.mu.Lock()
	ids := make([]string, 0, len(p.buttons))
	for id := range p.buttons {
		ids = append(ids, id)
	}
	p.mu.Unlock()
	for _, id := range ids {
		p.redraw(id)
	}
}

// redraw updates one key: an image for play/pause, a state for loop.
func (p *plugin) redraw(ctxID string) {
	p.mu.Lock()
	b, ok := p.buttons[ctxID]
	st := p.state
	p.mu.Unlock()
	if !ok {
		return
	}
	switch b.action {
	case actionPlayPause:
		p.drawPlayPause(ctxID, b.settings, st)
	case actionLoop:
		p.conn.SetState(p.ctx, ctxID, loopState(st))
	}
}

// drawPlayPause renders the key image and sends it.
func (p *plugin) drawPlayPause(ctxID string, s Settings, st player.State) {
	opts := s.renderOptions()
	info := render.Info{
		Title: st.Track.Title, Artist: st.Track.Artist, Duration: st.Track.Duration,
		Position: st.PositionAt(time.Now()), Playing: st.Playing,
		Remote: st.Source == player.SourceRemote, Unavailable: !st.Available(),
	}
	if opts.ShowArt && st.Track.ArtURL != "" && st.Available() {
		ctx, cancel := context.WithTimeout(p.ctx, 8*time.Second)
		img, err := p.art.Get(ctx, st.Track.ArtURL)
		cancel()
		if err != nil {
			p.debugf("art: %v", err)
		} else {
			info.Art = img
		}
	}
	if err := p.conn.SetImage(p.ctx, ctxID, render.DataURL(render.Key(info, opts))); err != nil {
		p.info.Printf("setImage for %s failed: %v", ctxID, err)
	}
}

// loopState maps the loop mode to the manifest's state index: off, all, one.
func loopState(st player.State) int {
	switch st.Loop {
	case player.LoopAll:
		return 1
	case player.LoopOne:
		return 2
	}
	return 0
}

func (p *plugin) debugf(format string, args ...any) {
	if p.debug != nil {
		p.debug.Printf(format, args...)
	}
}

// handlers wires the OpenDeck events.
func (p *plugin) handlers() openaction.Handlers {
	return openaction.Handlers{
		WillAppear: func(ctx context.Context, ev openaction.Event, pl openaction.AppearPayload) error {
			return p.track(ev, pl.Settings)
		},
		DidReceiveSettings: func(ctx context.Context, ev openaction.Event, pl openaction.SettingsPayload) error {
			return p.track(ev, pl.Settings)
		},
		WillDisappear: func(ctx context.Context, ev openaction.Event, pl openaction.AppearPayload) error {
			p.mu.Lock()
			delete(p.buttons, ev.Context)
			p.mu.Unlock()
			return nil
		},
		KeyDown: func(ctx context.Context, ev openaction.Event, pl openaction.KeyPayload) error {
			return p.onKeyDown(ev)
		},
		SendToPlugin: func(ctx context.Context, ev openaction.Event, payload json.RawMessage) error {
			p.rememberInspector(ev)
			return p.handleInspectorMessage(ev, payload)
		},
		PropertyInspectorDidAppear: func(ctx context.Context, ev openaction.Event) error {
			p.rememberInspector(ev)
			return p.sendStatus(ev)
		},
		Unknown: func(ctx context.Context, ev openaction.Event) error {
			if ev.Event == "propertyInspectorDidDisappear" {
				p.mu.Lock()
				if p.inspector != nil && p.inspector.Context == ev.Context {
					p.inspector = nil
				}
				p.mu.Unlock()
			}
			return nil
		},
	}
}

// rememberInspector records which key's panel is open.
func (p *plugin) rememberInspector(ev openaction.Event) {
	p.mu.Lock()
	e := ev
	p.inspector = &e
	p.mu.Unlock()
}

// track registers a key and draws it right away.
func (p *plugin) track(ev openaction.Event, raw json.RawMessage) error {
	if err := checkAction(ev.Action); err != nil {
		return err
	}
	s, err := decodeSettings(raw)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.buttons[ev.Context] = button{action: ev.Action, settings: s}
	p.mu.Unlock()
	p.info.Printf("key %s appeared: %s", ev.Context, shortAction(ev.Action))
	go p.redraw(ev.Context)
	return nil
}

// onKeyDown dispatches a press to the player in a goroutine.
func (p *plugin) onKeyDown(ev openaction.Event) error {
	if err := checkAction(ev.Action); err != nil {
		return err
	}
	if !p.inflight.begin(ev.Context) {
		return nil
	}
	go func() {
		defer p.inflight.end(ev.Context)
		ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
		defer cancel()
		p.mu.Lock()
		pl, st := p.player, p.state
		p.mu.Unlock()

		var err error
		switch ev.Action {
		case actionPlayPause:
			err = pl.PlayPause(ctx)
		case actionNext:
			err = pl.Next(ctx)
		case actionPrevious:
			err = pl.Previous(ctx)
		case actionLoop:
			err = pl.SetLoop(ctx, player.NextLoop(st.Loop))
		}
		if err != nil {
			p.info.Printf("%s failed: %v", shortAction(ev.Action), err)
			p.conn.ShowAlert(ctx, ev.Context)
			return
		}
		// No "ok" flash: the keys redraw from the state change, which is
		// feedback enough. Only failures are shown.
		p.info.Printf("%s sent (%s)", shortAction(ev.Action), st.Source)
	}()
	return nil
}

// hostname is this computer's name as Spotify lists it.
func hostname() string {
	h, _ := os.Hostname()
	return h
}
