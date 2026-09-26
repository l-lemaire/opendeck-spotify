package mpris

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// Well-known names from the MPRIS specification.
const (
	SpotifyBusName = "org.mpris.MediaPlayer2.spotify"
	objectPath     = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	playerIface    = "org.mpris.MediaPlayer2.Player"
	propsIface     = "org.freedesktop.DBus.Properties"
)

// Playback statuses as MPRIS spells them.
const (
	Playing = "Playing"
	Paused  = "Paused"
	Stopped = "Stopped"
)

// Loop modes as MPRIS spells them. The web API calls them off, track, context.
const (
	LoopNone     = "None"
	LoopTrack    = "Track"
	LoopPlaylist = "Playlist"
)

// ErrNotRunning is returned when the player owns no bus name, i.e. the
// desktop client is not started.
var ErrNotRunning = errors.New("spotify desktop client is not running")

// Track is what MPRIS knows about the current item.
type Track struct {
	ID     string // MPRIS track id, e.g. "/com/spotify/track/2g2a5k..."
	Title  string
	Artist string // artists joined with ", "
	Album  string
	ArtURL string // https://i.scdn.co/image/... for Spotify
	URL    string // https://open.spotify.com/track/...
	Length time.Duration
}

// State is a snapshot of the player.
type State struct {
	Status   string // Playing, Paused, Stopped
	Loop     string // None, Track, Playlist
	Shuffle  bool
	Position time.Duration // as reported at the time of the snapshot
	Track    Track
	// At is when the snapshot was taken; with Status == Playing the real
	// position is Position + (now - At).
	At time.Time
}

// Player is a connection to one MPRIS player.
type Player struct {
	conn    *dbus.Conn
	busName string
	obj     dbus.BusObject
	log     *log.Logger
}

// Connect opens the session bus and targets busName (SpotifyBusName for
// the real client; tests use a fake). The player need not be running yet.
func Connect(busName string, logger *log.Logger) (*Player, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("session bus: %w (is a desktop session running?)", err)
	}
	return &Player{conn: conn, busName: busName, obj: conn.Object(busName, objectPath), log: logger}, nil
}

// Close releases the bus connection.
func (p *Player) Close() error { return p.conn.Close() }

// Running reports whether the player currently owns its bus name.
func (p *Player) Running() (bool, error) {
	var has bool
	err := p.conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, p.busName).Store(&has)
	return has, err
}

// State reads every player property in one call.
func (p *Player) State(ctx context.Context) (State, error) {
	var props map[string]dbus.Variant
	if err := p.obj.CallWithContext(ctx, propsIface+".GetAll", 0, playerIface).Store(&props); err != nil {
		return State{}, p.wrap("GetAll", err)
	}
	debugf(p.log, "mpris: GetAll -> %d properties", len(props))
	st := stateFromProps(props)
	st.At = time.Now()
	return st, nil
}

// Position re-reads only the position, the one value that is not pushed.
func (p *Player) Position(ctx context.Context) (time.Duration, error) {
	v, err := p.obj.GetProperty(playerIface + ".Position")
	if err != nil {
		return 0, p.wrap("Position", err)
	}
	return microseconds(v.Value()), nil
}

// PlayPause, Next and Previous are the transport controls.
func (p *Player) PlayPause(ctx context.Context) error { return p.call(ctx, "PlayPause") }
func (p *Player) Next(ctx context.Context) error      { return p.call(ctx, "Next") }
func (p *Player) Previous(ctx context.Context) error  { return p.call(ctx, "Previous") }

// SetLoop asks the player to change its loop mode. Spotify ignores this
// (see the package comment); it is here for players that honour it.
func (p *Player) SetLoop(ctx context.Context, mode string) error {
	debugf(p.log, "mpris: set LoopStatus=%s", mode)
	if err := p.obj.SetProperty(playerIface+".LoopStatus", dbus.MakeVariant(mode)); err != nil {
		return p.wrap("set LoopStatus", err)
	}
	return nil
}

func (p *Player) call(ctx context.Context, method string) error {
	debugf(p.log, "mpris: call %s", method)
	if err := p.obj.CallWithContext(ctx, playerIface+"."+method, 0).Err; err != nil {
		return p.wrap(method, err)
	}
	return nil
}

// wrap turns the bus's "no such name" error into ErrNotRunning.
func (p *Player) wrap(what string, err error) error {
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) && (dbusErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown" || dbusErr.Name == "org.freedesktop.DBus.Error.NameHasNoOwner") {
		return ErrNotRunning
	}
	return fmt.Errorf("mpris %s: %w", what, err)
}

// stateFromProps decodes the property map. Missing entries leave zero
// values, so a player with sparse metadata still yields a usable State.
func stateFromProps(props map[string]dbus.Variant) State {
	var st State
	if v, ok := props["PlaybackStatus"]; ok {
		st.Status, _ = v.Value().(string)
	}
	if v, ok := props["LoopStatus"]; ok {
		st.Loop, _ = v.Value().(string)
	}
	if v, ok := props["Shuffle"]; ok {
		st.Shuffle, _ = v.Value().(bool)
	}
	if v, ok := props["Position"]; ok {
		st.Position = microseconds(v.Value())
	}
	if v, ok := props["Metadata"]; ok {
		if md, ok := v.Value().(map[string]dbus.Variant); ok {
			st.Track = trackFromMetadata(md)
		}
	}
	return st
}

// trackFromMetadata decodes the Metadata dictionary. Keys follow the
// "mpris:" and "xesam:" vocabularies of the specification.
func trackFromMetadata(md map[string]dbus.Variant) Track {
	var t Track
	str := func(key string) string {
		v, ok := md[key]
		if !ok {
			return ""
		}
		switch x := v.Value().(type) {
		case string:
			return x
		case dbus.ObjectPath:
			return string(x)
		}
		return ""
	}
	t.ID = str("mpris:trackid")
	t.Title = str("xesam:title")
	t.Album = str("xesam:album")
	t.ArtURL = str("mpris:artUrl")
	t.URL = str("xesam:url")
	if v, ok := md["xesam:artist"]; ok {
		if artists, ok := v.Value().([]string); ok {
			t.Artist = strings.Join(artists, ", ")
		}
	}
	if v, ok := md["mpris:length"]; ok {
		t.Length = microseconds(v.Value())
	}
	return t
}

// microseconds converts the integer types MPRIS uses for durations.
func microseconds(v any) time.Duration {
	switch x := v.(type) {
	case int64:
		return time.Duration(x) * time.Microsecond
	case uint64:
		return time.Duration(x) * time.Microsecond
	case int32:
		return time.Duration(x) * time.Microsecond
	case uint32:
		return time.Duration(x) * time.Microsecond
	}
	return 0
}

func debugf(l *log.Logger, format string, args ...any) {
	if l != nil {
		l.Printf(format, args...)
	}
}
