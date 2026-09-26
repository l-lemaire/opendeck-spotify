package mpris

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

// Event is what Watch reports.
type Event struct {
	// Running is false when the client left the bus (quit) and true when it
	// appeared; State is then empty or a fresh snapshot respectively.
	Running bool
	State   State
	// Seeked is set when the position jumped without other changes.
	Seeked bool
}

// Watch subscribes to the player's change notifications and calls handle
// with a fresh snapshot after each one, until ctx ends. It also reports the
// client starting or quitting. The first call happens immediately with the
// current state.
//
// Under the hood three signals are matched: PropertiesChanged on the player
// object (state, track, loop...), Seeked (position jumps), and the bus's
// NameOwnerChanged for our bus name (start/quit).
func (p *Player) Watch(ctx context.Context, handle func(Event)) error {
	// Match rules tell the bus daemon which signals to forward to us.
	matches := [][]dbus.MatchOption{
		{dbus.WithMatchObjectPath(objectPath), dbus.WithMatchInterface(propsIface), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchSender(p.busName)},
		{dbus.WithMatchObjectPath(objectPath), dbus.WithMatchInterface(playerIface), dbus.WithMatchMember("Seeked"), dbus.WithMatchSender(p.busName)},
		{dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, p.busName)},
	}
	for _, m := range matches {
		if err := p.conn.AddMatchSignalContext(ctx, m...); err != nil {
			return err
		}
	}
	defer func() {
		for _, m := range matches {
			p.conn.RemoveMatchSignal(m...)
		}
	}()

	signals := make(chan *dbus.Signal, 32)
	p.conn.Signal(signals)
	defer p.conn.RemoveSignal(signals)

	// Initial snapshot.
	p.emit(ctx, handle, false)

	for {
		select {
		case <-ctx.Done():
			return nil
		case sig, ok := <-signals:
			if !ok {
				return nil
			}
			switch sig.Name {
			case "org.freedesktop.DBus.NameOwnerChanged":
				// args: name, old owner, new owner. Empty new owner = quit.
				if len(sig.Body) == 3 {
					newOwner, _ := sig.Body[2].(string)
					debugf(p.log, "mpris: %s owner changed -> %q", p.busName, newOwner)
					if newOwner == "" {
						handle(Event{Running: false})
						continue
					}
					// The client just started; give it a moment to publish
					// properties before reading them.
					time.Sleep(200 * time.Millisecond)
					p.emit(ctx, handle, false)
				}
			case playerIface + ".Seeked":
				debugf(p.log, "mpris: Seeked")
				p.emit(ctx, handle, true)
			default: // PropertiesChanged
				debugf(p.log, "mpris: PropertiesChanged")
				p.emit(ctx, handle, false)
			}
		}
	}
}

// emit reads the state and hands it to the handler, or reports "not
// running" when the read fails because the client is gone.
func (p *Player) emit(ctx context.Context, handle func(Event), seeked bool) {
	st, err := p.State(ctx)
	if err != nil {
		debugf(p.log, "mpris: state unavailable: %v", err)
		handle(Event{Running: false})
		return
	}
	handle(Event{Running: true, State: st, Seeked: seeked})
}
