// Package player merges the two ways of reaching Spotify into one model:
//
//   - local: the desktop client on this machine, over MPRIS. Instant,
//     event-driven, no account.
//   - remote: Spotify's web API. Any device, polled, needs a login.
//
// The rule for which one is in charge, evaluated on every change:
//
//  1. The local client is running and playing: local.
//  2. Otherwise the web API is available and reports playback on another
//     device: remote (music is playing or paused elsewhere).
//  3. Otherwise the local client is running (paused): local.
//  4. Otherwise the web API reports something: remote. Else nothing.
//
// Loop mode always goes through the web API, since Spotify ignores it over
// MPRIS; for the local client the request names this computer's device.
package player

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/mpris"
	"github.com/l-lemaire/opendeck-spotify/internal/spotifyapi"
)

// Source says who is in charge.
type Source string

const (
	SourceNone   Source = "none"
	SourceLocal  Source = "local"
	SourceRemote Source = "remote"
)

// Loop modes in the words the CLI and the plugin use.
const (
	LoopOff = "off"
	LoopAll = "all"
	LoopOne = "one"
)

// Track is the current item, whichever source it came from.
type Track struct {
	ID       string
	Title    string
	Artist   string
	Album    string
	ArtURL   string
	Duration time.Duration
}

// State is the merged snapshot.
type State struct {
	Source  Source
	Playing bool
	Loop    string // off, all, one
	Shuffle bool
	Track   Track
	// Device names the remote device when Source is remote; empty locally.
	Device string
	// Position and At: the position as read, and when. Use PositionAt for
	// the live value.
	Position time.Duration
	At       time.Time
}

// Available reports whether there is anything to show or control.
func (s State) Available() bool { return s.Source != SourceNone }

// PositionAt returns the position at time now, advanced locally while
// playing and capped at the track length.
func (s State) PositionAt(now time.Time) time.Duration {
	p := s.Position
	if s.Playing && !s.At.IsZero() {
		p += now.Sub(s.At)
	}
	if s.Track.Duration > 0 && p > s.Track.Duration {
		p = s.Track.Duration
	}
	if p < 0 {
		p = 0
	}
	return p
}

// Options wires the sources. Either may be nil.
type Options struct {
	Local  *mpris.Player
	Remote *spotifyapi.Client
	// LocalDeviceName is how this computer appears in Spotify's device
	// list (its host name). Empty means os.Hostname().
	LocalDeviceName string
	// RemotePoll is the polling interval when the remote source matters;
	// zero means 2 s. While the local client plays, polling slows to
	// RemoteIdlePoll (zero means 15 s) just to notice a hand-over.
	RemotePoll     time.Duration
	RemoteIdlePoll time.Duration
	Log            *log.Logger
}

// ErrNeedsAPI is returned by SetLoop without a web API login.
var ErrNeedsAPI = errors.New("changing the loop mode needs the Spotify web API login")

// ErrNothingToControl is returned when neither source has a player.
var ErrNothingToControl = errors.New("nothing to control: Spotify is not running here and nothing plays elsewhere")

// Player is the merged model.
type Player struct {
	o Options

	mu           sync.Mutex
	local        *mpris.State // nil when the client is not running
	remote       *spotifyapi.State
	state        State
	onChange     func(State)
	localDevice  string // resolved device id of this computer, cached
	localRunning bool
	notified     bool // the first evaluation always notifies
}

// New builds a player; call Run to start following the sources.
func New(o Options) *Player {
	if o.RemotePoll <= 0 {
		o.RemotePoll = 2 * time.Second
	}
	if o.RemoteIdlePoll <= 0 {
		o.RemoteIdlePoll = 15 * time.Second
	}
	if o.LocalDeviceName == "" {
		o.LocalDeviceName, _ = os.Hostname()
	}
	return &Player{o: o, state: State{Source: SourceNone}}
}

// State returns the latest merged snapshot.
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Run follows both sources until ctx ends, calling onChange whenever the
// merged state changes. It returns when ctx is done.
func (p *Player) Run(ctx context.Context, onChange func(State)) {
	p.mu.Lock()
	p.onChange = onChange
	p.mu.Unlock()

	var wg sync.WaitGroup
	if p.o.Local != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Watch returns only when ctx ends; a bus failure is logged and
			// retried since the plugin runs for hours.
			for ctx.Err() == nil {
				err := p.o.Local.Watch(ctx, p.onLocalEvent)
				if err != nil && ctx.Err() == nil {
					debugf(p.o.Log, "player: local watch failed: %v; retrying in 5s", err)
					select {
					case <-ctx.Done():
					case <-time.After(5 * time.Second):
					}
				}
			}
		}()
	}
	if p.o.Remote != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.pollRemote(ctx)
		}()
	}
	if p.o.Local == nil && p.o.Remote == nil {
		p.recompute()
	}
	wg.Wait()
}

func (p *Player) onLocalEvent(e mpris.Event) {
	p.mu.Lock()
	p.localRunning = e.Running
	if e.Running {
		st := e.State
		p.local = &st
	} else {
		p.local = nil
	}
	p.mu.Unlock()
	p.recompute()
}

// pollRemote asks the web API at an interval that depends on who is in
// charge: often when remote matters, rarely when the local client plays.
func (p *Player) pollRemote(ctx context.Context) {
	for {
		st, err := p.o.Remote.PlaybackState(ctx)
		p.mu.Lock()
		if err != nil {
			debugf(p.o.Log, "player: remote poll failed: %v", err)
			// Keep the last known remote state on transient errors.
		} else if st.Active {
			p.remote = &st
		} else {
			p.remote = nil
		}
		localPlaying := p.local != nil && p.local.Status == mpris.Playing
		p.mu.Unlock()
		p.recompute()

		wait := p.o.RemotePoll
		if localPlaying {
			wait = p.o.RemoteIdlePoll
		}
		var apiErr *spotifyapi.APIError
		if errors.As(err, &apiErr) && apiErr.RetryAfter > wait {
			wait = apiErr.RetryAfter
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// recompute applies the source rule and notifies on change.
func (p *Player) recompute() {
	p.mu.Lock()
	local, remote := p.local, p.remote
	remoteElsewhere := remote != nil && !strings.EqualFold(remote.Device.Name, p.o.LocalDeviceName)

	var st State
	switch {
	case local != nil && local.Status == mpris.Playing:
		st = fromLocal(*local)
	case remoteElsewhere:
		st = fromRemote(*remote)
	case local != nil:
		st = fromLocal(*local)
	case remote != nil:
		st = fromRemote(*remote)
	default:
		st = State{Source: SourceNone, At: time.Now()}
	}
	changed := !p.notified || !sameState(p.state, st) || jumped(p.state, st)
	p.state = st
	p.notified = true
	notify := p.onChange
	p.mu.Unlock()

	if changed && notify != nil {
		debugf(p.o.Log, "player: source=%s playing=%v loop=%s track=%q pos=%s", st.Source, st.Playing, st.Loop, st.Track.Title, st.Position)
		notify(st)
	}
}

// sameState ignores the timestamp and the position: those change on every
// read without meaning anything happened.
func sameState(a, b State) bool {
	a.At, b.At, a.Position, b.Position = time.Time{}, time.Time{}, 0, 0
	return a == b
}

// jumped reports whether the new snapshot's position disagrees with where
// the old one would have advanced to: a seek, a replay from the start, or
// drift on a remote device. Small differences are the normal jitter of
// reading a clock twice.
func jumped(old, cur State) bool {
	if old.At.IsZero() {
		return false
	}
	expected := old.PositionAt(cur.At)
	diff := cur.Position - expected
	if diff < 0 {
		diff = -diff
	}
	return diff > 1500*time.Millisecond
}

func fromLocal(l mpris.State) State {
	return State{
		Source: SourceLocal, Playing: l.Status == mpris.Playing, Loop: loopFromMPRIS(l.Loop), Shuffle: l.Shuffle,
		Track:    Track{ID: l.Track.ID, Title: l.Track.Title, Artist: l.Track.Artist, Album: l.Track.Album, ArtURL: l.Track.ArtURL, Duration: l.Track.Length},
		Position: l.Position, At: l.At,
	}
}

func fromRemote(r spotifyapi.State) State {
	return State{
		Source: SourceRemote, Playing: r.Playing, Loop: loopFromAPI(r.Repeat), Shuffle: r.Shuffle, Device: r.Device.Name,
		Track:    Track{ID: r.Track.ID, Title: r.Track.Title, Artist: r.Track.Artist, Album: r.Track.Album, ArtURL: r.Track.ArtURL, Duration: r.Track.Duration},
		Position: r.Progress, At: r.At,
	}
}

// Controls. They act on the source in charge and let the next event or
// poll update the state.

func (p *Player) PlayPause(ctx context.Context) error {
	switch p.State().Source {
	case SourceLocal:
		return p.o.Local.PlayPause(ctx)
	case SourceRemote:
		_, err := p.o.Remote.PlayPause(ctx)
		return err
	}
	return ErrNothingToControl
}

func (p *Player) Next(ctx context.Context) error {
	switch p.State().Source {
	case SourceLocal:
		return p.o.Local.Next(ctx)
	case SourceRemote:
		return p.o.Remote.Next(ctx, "")
	}
	return ErrNothingToControl
}

func (p *Player) Previous(ctx context.Context) error {
	switch p.State().Source {
	case SourceLocal:
		return p.o.Local.Previous(ctx)
	case SourceRemote:
		return p.o.Remote.Previous(ctx, "")
	}
	return ErrNothingToControl
}

// SetLoop changes the loop mode through the web API. For the local client
// the request names this computer, which also works while paused.
func (p *Player) SetLoop(ctx context.Context, mode string) error {
	if p.o.Remote == nil {
		return ErrNeedsAPI
	}
	st := p.State()
	deviceID := ""
	if st.Source == SourceLocal {
		id, err := p.localDeviceID(ctx)
		if err != nil {
			return err
		}
		deviceID = id
	} else if st.Source == SourceNone {
		return ErrNothingToControl
	}
	return p.o.Remote.SetRepeat(ctx, loopToAPI(mode), deviceID)
}

// NextLoop returns the mode after `mode` in the cycle off -> all -> one.
func NextLoop(mode string) string {
	switch mode {
	case LoopOff:
		return LoopAll
	case LoopAll:
		return LoopOne
	default:
		return LoopOff
	}
}

// localDeviceID finds this computer in Spotify's device list, once.
func (p *Player) localDeviceID(ctx context.Context) (string, error) {
	p.mu.Lock()
	cached := p.localDevice
	p.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	devices, err := p.o.Remote.Devices(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range devices {
		if strings.EqualFold(d.Name, p.o.LocalDeviceName) {
			p.mu.Lock()
			p.localDevice = d.ID
			p.mu.Unlock()
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("this computer (%q) is not in Spotify's device list; play something in the client first", p.o.LocalDeviceName)
}

func loopFromMPRIS(l string) string {
	switch l {
	case mpris.LoopTrack:
		return LoopOne
	case mpris.LoopPlaylist:
		return LoopAll
	}
	return LoopOff
}

func loopFromAPI(r string) string {
	switch r {
	case spotifyapi.RepeatTrack:
		return LoopOne
	case spotifyapi.RepeatContext:
		return LoopAll
	}
	return LoopOff
}

func loopToAPI(mode string) string {
	switch mode {
	case LoopOne:
		return spotifyapi.RepeatTrack
	case LoopAll:
		return spotifyapi.RepeatContext
	}
	return spotifyapi.RepeatOff
}

func debugf(l *log.Logger, format string, args ...any) {
	if l != nil {
		l.Printf(format, args...)
	}
}
