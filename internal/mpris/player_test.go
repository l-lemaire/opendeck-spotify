package mpris

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/mpris/mpristest"
)

func testLogger(t *testing.T) *log.Logger {
	if testing.Verbose() {
		return log.New(os.Stderr, "    debug: ", 0)
	}
	return nil
}

func TestStateAndControls(t *testing.T) {
	fake := mpristest.New(t)
	p, err := Connect(fake.BusName, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx := context.Background()

	if running, err := p.Running(); err != nil || !running {
		t.Fatalf("Running = %v, %v", running, err)
	}
	st, err := p.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != Paused || st.Loop != LoopPlaylist || st.Position != 5294*time.Millisecond {
		t.Errorf("state = %+v", st)
	}
	if st.Track.Title != "Lifestyles of the Rich & Famous" || st.Track.Artist != "Good Charlotte" || st.Track.Length != 190466*time.Millisecond || st.Track.ID != "/com/spotify/track/FAKE1" {
		t.Errorf("track = %+v", st.Track)
	}

	if err := p.PlayPause(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Previous(ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PlayPause", "Next", "Previous"} {
		select {
		case got := <-fake.Calls:
			if got != want {
				t.Errorf("call = %s, want %s", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s never reached the player", want)
		}
	}
	st, _ = p.State(ctx)
	if st.Status != Playing {
		t.Errorf("after PlayPause status = %s", st.Status)
	}
}

func TestNotRunning(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("no session bus")
	}
	p, err := Connect("org.mpris.MediaPlayer2.nobody_here", nil)
	if err != nil {
		t.Skip(err)
	}
	defer p.Close()
	if running, _ := p.Running(); running {
		t.Fatal("unexpected owner")
	}
	if _, err := p.State(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Errorf("State = %v, want ErrNotRunning", err)
	}
	if err := p.PlayPause(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Errorf("PlayPause = %v, want ErrNotRunning", err)
	}
}

func TestWatch(t *testing.T) {
	fake := mpristest.New(t)
	p, err := Connect(fake.BusName, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan Event, 16)
	go p.Watch(ctx, func(e Event) { events <- e })

	next := func(what string) Event {
		select {
		case e := <-events:
			return e
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: no event", what)
			return Event{}
		}
	}
	if e := next("initial"); !e.Running || e.State.Status != Paused {
		t.Errorf("initial = %+v", e)
	}
	fake.SetStatus("Playing")
	if e := next("status"); !e.Running || e.State.Status != Playing {
		t.Errorf("status change = %+v", e)
	}
	fake.SetTitle("Anthem")
	if e := next("title"); e.State.Track.Title != "Anthem" {
		t.Errorf("title change = %+v", e)
	}
	fake.SetPosition(42 * time.Second)
	if e := next("seek"); !e.Seeked || e.State.Position != 42*time.Second {
		t.Errorf("seek = %+v", e)
	}
	fake.Quit()
	if e := next("quit"); e.Running {
		t.Errorf("after quit = %+v", e)
	}
}
