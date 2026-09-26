package player

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/mpris"
	"github.com/l-lemaire/opendeck-spotify/internal/mpris/mpristest"
	"github.com/l-lemaire/opendeck-spotify/internal/secrets"
	"github.com/l-lemaire/opendeck-spotify/internal/spotifyapi"
	"github.com/l-lemaire/opendeck-spotify/internal/spotifyapi/spotifyapitest"
)

type memStore map[string]string

func (m memStore) Name() string { return "memory" }
func (m memStore) Get(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (m memStore) Set(k, v string) error { m[k] = v; return nil }
func (m memStore) Delete(k string) error { delete(m, k); return nil }

func remoteClient(t *testing.T, fake *spotifyapitest.Server) *spotifyapi.Client {
	oldAuth, oldToken, oldAPI := spotifyapi.AuthorizeURL, spotifyapi.TokenURL, spotifyapi.APIBase
	spotifyapi.AuthorizeURL, spotifyapi.TokenURL, spotifyapi.APIBase = fake.URL+"/authorize", fake.URL+"/api/token", fake.URL+"/v1"
	t.Cleanup(func() { spotifyapi.AuthorizeURL, spotifyapi.TokenURL, spotifyapi.APIBase = oldAuth, oldToken, oldAPI })
	store := memStore{}
	spotifyapi.SaveTokens(store, spotifyapi.Tokens{AccessToken: "ACCESS-1", RefreshToken: "REFRESH-1", Expiry: time.Now().Add(time.Hour)})
	c, err := spotifyapi.NewClient(spotifyapi.ClientOptions{ClientID: fake.ClientID, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// start runs the player and returns a channel of state changes.
func start(t *testing.T, o Options) (*Player, chan State, context.CancelFunc) {
	o.RemotePoll = 100 * time.Millisecond
	o.RemoteIdlePoll = 100 * time.Millisecond
	p := New(o)
	states := make(chan State, 32)
	ctx, cancel := context.WithCancel(context.Background())
	go p.Run(ctx, func(s State) { states <- s })
	return p, states, cancel
}

func waitFor(t *testing.T, states chan State, what string, ok func(State) bool) State {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-states:
			if ok(s) {
				return s
			}
		case <-deadline:
			t.Fatalf("%s: no matching state within 5s", what)
			return State{}
		}
	}
}

func TestLocalOnly(t *testing.T) {
	fake := mpristest.New(t)
	local, err := mpris.Connect(fake.BusName, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	p, states, cancel := start(t, Options{Local: local, LocalDeviceName: "alpha"})
	defer cancel()

	st := waitFor(t, states, "initial", func(s State) bool { return s.Source == SourceLocal })
	if st.Playing || st.Loop != LoopAll || st.Track.Title != "Lifestyles of the Rich & Famous" || st.Position != 5294*time.Millisecond {
		t.Errorf("state = %+v", st)
	}
	// PositionAt advances only while playing.
	if got := st.PositionAt(st.At.Add(10 * time.Second)); got != st.Position {
		t.Errorf("paused position advanced to %s", got)
	}
	fake.SetStatus("Playing")
	st = waitFor(t, states, "playing", func(s State) bool { return s.Playing })
	if got := st.PositionAt(st.At.Add(10 * time.Second)); got != st.Position+10*time.Second {
		t.Errorf("playing position = %s, want +10s", got)
	}
	if got := st.PositionAt(st.At.Add(time.Hour)); got != st.Track.Duration {
		t.Errorf("position should cap at duration, got %s", got)
	}

	if err := p.PlayPause(context.Background()); err != nil {
		t.Fatal(err)
	}
	if call := <-fake.Calls; call != "PlayPause" {
		t.Errorf("call = %s", call)
	}
	if err := p.SetLoop(context.Background(), LoopOne); !errors.Is(err, ErrNeedsAPI) {
		t.Errorf("SetLoop without API = %v, want ErrNeedsAPI", err)
	}

	fake.Quit()
	waitFor(t, states, "quit", func(s State) bool { return s.Source == SourceNone })
	if err := p.Next(context.Background()); !errors.Is(err, ErrNothingToControl) {
		t.Errorf("Next with nothing = %v", err)
	}
}

func TestRemoteTakesOverWhenLocalPausedAndMusicElsewhere(t *testing.T) {
	mp := mpristest.New(t)
	local, err := mpris.Connect(mp.BusName, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	api := spotifyapitest.New(t) // playing on "Living room"
	remote := remoteClient(t, api)

	p, states, cancel := start(t, Options{Local: local, Remote: remote, LocalDeviceName: "alpha"})
	defer cancel()

	// Local is paused, remote plays elsewhere: remote wins.
	st := waitFor(t, states, "remote", func(s State) bool { return s.Source == SourceRemote })
	if !st.Playing || st.Device != "Living room" || st.Track.ArtURL != "https://i.scdn.co/image/large" {
		t.Errorf("remote state = %+v", st)
	}
	// Controls go to the API.
	if err := p.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls := api.Calls(); len(calls) != 1 || calls[0][:len("POST /v1/me/player/next")] != "POST /v1/me/player/next" {
		t.Errorf("api calls = %v", calls)
	}
	// Loop through the API, on the active device (no device_id).
	if err := p.SetLoop(context.Background(), LoopOne); err != nil {
		t.Fatal(err)
	}
	calls := api.Calls()
	if last := calls[len(calls)-1]; !contains(last, "repeat") || !contains(last, "state=track") || contains(last, "device_id") {
		t.Errorf("repeat call = %s", last)
	}

	// The local client starts playing: local takes over immediately.
	mp.SetStatus("Playing")
	st = waitFor(t, states, "local playing", func(s State) bool { return s.Source == SourceLocal && s.Playing })
	if st.Device != "" {
		t.Errorf("local state has device %q", st.Device)
	}
	// Loop for the local client names this computer's device id.
	api.Device = "alpha" // the device list's active entry is now this computer
	if err := p.SetLoop(context.Background(), LoopOff); err != nil {
		t.Fatal(err)
	}
	calls = api.Calls()
	if last := calls[len(calls)-1]; !contains(last, "device_id=dev-1") || !contains(last, "state=off") {
		t.Errorf("local repeat call = %s", last)
	}
}

func TestRemoteOnlyAndNothing(t *testing.T) {
	api := spotifyapitest.New(t)
	api.Active = false
	remote := remoteClient(t, api)
	p, states, cancel := start(t, Options{Remote: remote, LocalDeviceName: "alpha"})
	defer cancel()

	waitFor(t, states, "nothing", func(s State) bool { return s.Source == SourceNone })
	if err := p.PlayPause(context.Background()); !errors.Is(err, ErrNothingToControl) {
		t.Errorf("PlayPause = %v", err)
	}
	api.Active = true
	st := waitFor(t, states, "remote appears", func(s State) bool { return s.Source == SourceRemote })
	if st.Device != "Living room" {
		t.Errorf("state = %+v", st)
	}
	if NextLoop(LoopOff) != LoopAll || NextLoop(LoopAll) != LoopOne || NextLoop(LoopOne) != LoopOff {
		t.Error("NextLoop cycle wrong")
	}
}

func contains(s, sub string) bool { return len(sub) <= len(s) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
