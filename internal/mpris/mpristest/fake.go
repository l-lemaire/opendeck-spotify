// Package mpristest provides a fake MPRIS player on the session bus for
// tests. It needs a session bus, so tests skip when none is available.
package mpristest

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	path        = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	playerIface = "org.mpris.MediaPlayer2.Player"
)

// Player is the fake. Exported fields record what was called.
type Player struct {
	BusName string
	conn    *dbus.Conn
	props   *prop.Properties
	Calls   chan string // "PlayPause", "Next", "Previous"
}

// New registers a fake player under a unique bus name. It starts paused on
// a sample track at 5 seconds.
func New(t *testing.T) *Player {
	t.Helper()
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("no session bus")
	}
	// A private connection per fake, so tests do not share signal handlers
	// with the client under test.
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Skip("session bus unavailable:", err)
	}
	f := &Player{BusName: fmt.Sprintf("org.mpris.MediaPlayer2.fake%d", time.Now().UnixNano()%1_000_000), conn: conn, Calls: make(chan string, 16)}

	metadata := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/com/spotify/track/FAKE1")),
		"mpris:length":  dbus.MakeVariant(uint64(190_466_000)),
		"mpris:artUrl":  dbus.MakeVariant("https://i.scdn.co/image/fake"),
		"xesam:title":   dbus.MakeVariant("Lifestyles of the Rich & Famous"),
		"xesam:artist":  dbus.MakeVariant([]string{"Good Charlotte"}),
		"xesam:album":   dbus.MakeVariant("The Young and The Hopeless"),
	}
	// prop.Map: interface -> property name -> definition. Emit: true means
	// changing the value sends PropertiesChanged, like a real player.
	propsMap := prop.Map{playerIface: {
		"PlaybackStatus": {Value: "Paused", Writable: false, Emit: prop.EmitTrue},
		"LoopStatus":     {Value: "Playlist", Writable: true, Emit: prop.EmitTrue},
		"Shuffle":        {Value: false, Writable: true, Emit: prop.EmitTrue},
		"Position":       {Value: int64(5_294_000), Writable: false, Emit: prop.EmitFalse},
		"Metadata":       {Value: metadata, Writable: false, Emit: prop.EmitTrue},
		"CanControl":     {Value: true, Writable: false, Emit: prop.EmitFalse},
	}}
	f.props = prop.New(conn, path, propsMap)
	if err := conn.Export(f, path, playerIface); err != nil {
		t.Fatal(err)
	}
	conn.Export(introspect.Introspectable(""), path, "org.freedesktop.DBus.Introspectable")
	reply, err := conn.RequestName(f.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request name: %v %v", reply, err)
	}
	t.Cleanup(func() { conn.Close() })
	return f
}

// Methods exported over D-Bus. The *dbus.Error return is the calling
// convention godbus expects.
func (f *Player) PlayPause() *dbus.Error {
	f.Calls <- "PlayPause"
	cur, _ := f.props.Get(playerIface, "PlaybackStatus")
	next := "Playing"
	if cur.Value() == "Playing" {
		next = "Paused"
	}
	f.props.SetMust(playerIface, "PlaybackStatus", next)
	return nil
}
func (f *Player) Next() *dbus.Error     { f.Calls <- "Next"; return nil }
func (f *Player) Previous() *dbus.Error { f.Calls <- "Previous"; return nil }

// SetStatus changes the playback status as if the user clicked in the app.
func (f *Player) SetStatus(status string) { f.props.SetMust(playerIface, "PlaybackStatus", status) }

// SetTitle changes the track title (a new Metadata value, hence a signal).
func (f *Player) SetTitle(title string) {
	v, _ := f.props.Get(playerIface, "Metadata")
	md := v.Value().(map[string]dbus.Variant)
	md["xesam:title"] = dbus.MakeVariant(title)
	f.props.SetMust(playerIface, "Metadata", md)
}

// SetPosition changes the position and emits Seeked, like a real seek.
func (f *Player) SetPosition(pos time.Duration) {
	f.props.SetMust(playerIface, "Position", int64(pos/time.Microsecond))
	f.conn.Emit(path, playerIface+".Seeked", int64(pos/time.Microsecond))
}

// Quit releases the bus name, as if the client exited.
func (f *Player) Quit() { f.conn.ReleaseName(f.BusName) }
