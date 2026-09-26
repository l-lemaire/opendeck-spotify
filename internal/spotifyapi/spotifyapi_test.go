package spotifyapi

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/secrets"
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

func testLogger(t *testing.T) *log.Logger {
	if testing.Verbose() {
		return log.New(os.Stderr, "    debug: ", 0)
	}
	return nil
}

// useFake points the package endpoints at the fake server for one test.
func useFake(t *testing.T, s *spotifyapitest.Server) {
	oldAuth, oldToken, oldAPI := AuthorizeURL, TokenURL, APIBase
	AuthorizeURL = s.URL + "/authorize"
	TokenURL = s.URL + "/api/token"
	APIBase = s.URL + "/v1"
	t.Cleanup(func() { AuthorizeURL, TokenURL, APIBase = oldAuth, oldToken, oldAPI })
}

func TestLoginPKCE(t *testing.T) {
	fake := spotifyapitest.New(t)
	useFake(t, fake)

	// The "browser": parse the authorization URL, check the PKCE fields,
	// then hit the callback like Spotify would after the user approves.
	browser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		if q.Get("client_id") != fake.ClientID || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 40 || q.Get("response_type") != "code" {
			t.Errorf("authorization URL query = %v", q)
		}
		if !strings.Contains(q.Get("scope"), "user-modify-playback-state") {
			t.Errorf("scope = %q", q.Get("scope"))
		}
		cb := q.Get("redirect_uri") + "?code=GOODCODE&state=" + q.Get("state")
		go func() {
			resp, err := http.Get(cb)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	tokens, err := Login(context.Background(), LoginOptions{ClientID: fake.ClientID, Port: 18765, OpenBrowser: browser, Timeout: 5 * time.Second, Log: testLogger(t)})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.Expired() {
		t.Errorf("tokens = %+v", tokens)
	}
}

func TestLoginRejectsWrongState(t *testing.T) {
	fake := spotifyapitest.New(t)
	useFake(t, fake)
	browser := func(authURL string) error {
		u, _ := url.Parse(authURL)
		go http.Get(u.Query().Get("redirect_uri") + "?code=GOODCODE&state=WRONG")
		return nil
	}
	_, err := Login(context.Background(), LoginOptions{ClientID: fake.ClientID, Port: 18766, OpenBrowser: browser, Timeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("got %v, want a state error", err)
	}
}

func loggedIn(t *testing.T, fake *spotifyapitest.Server) (*Client, memStore) {
	store := memStore{}
	SaveTokens(store, Tokens{AccessToken: "ACCESS-1", RefreshToken: "REFRESH-1", Expiry: time.Now().Add(time.Hour)})
	c, err := NewClient(ClientOptions{ClientID: fake.ClientID, Store: store, Log: testLogger(t)})
	if err != nil {
		t.Fatal(err)
	}
	return c, store
}

func TestPlaybackStateAndControls(t *testing.T) {
	fake := spotifyapitest.New(t)
	useFake(t, fake)
	c, _ := loggedIn(t, fake)
	ctx := context.Background()

	st, err := c.PlaybackState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Active || !st.Playing || st.Repeat != RepeatOff || st.Progress != 42*time.Second || st.Device.Name != "Living room" {
		t.Errorf("state = %+v", st)
	}
	if st.Track.Title != "Lifestyles of the Rich & Famous" || st.Track.Artist != "Good Charlotte" || st.Track.Duration != 190466*time.Millisecond || st.Track.ArtURL != "https://i.scdn.co/image/large" {
		t.Errorf("track = %+v", st.Track)
	}

	if playing, err := c.PlayPause(ctx); err != nil || playing {
		t.Fatalf("PlayPause = %v, %v; want paused", playing, err)
	}
	if err := c.Next(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRepeat(ctx, RepeatTrack, "dev-1"); err != nil {
		t.Fatal(err)
	}
	st, _ = c.PlaybackState(ctx)
	if st.Playing || st.Repeat != RepeatTrack {
		t.Errorf("after controls: playing=%v repeat=%s", st.Playing, st.Repeat)
	}
	calls := fake.Calls()
	if len(calls) != 3 || !strings.HasPrefix(calls[0], "PUT /v1/me/player/pause") || !strings.Contains(calls[2], "state=track") || !strings.Contains(calls[2], "device_id=dev-1") {
		t.Errorf("calls = %v", calls)
	}

	devices, err := c.Devices(ctx)
	if err != nil || len(devices) != 2 || devices[0].Name != "Living room" {
		t.Errorf("devices = %+v, %v", devices, err)
	}
}

func TestNothingPlaying(t *testing.T) {
	fake := spotifyapitest.New(t)
	useFake(t, fake)
	fake.Active = false
	c, _ := loggedIn(t, fake)
	st, err := c.PlaybackState(context.Background())
	if err != nil || st.Active {
		t.Fatalf("state = %+v, %v; want inactive", st, err)
	}
	var apiErr *APIError
	if err := c.Next(context.Background(), ""); !errors.As(err, &apiErr) || apiErr.Reason != "NO_ACTIVE_DEVICE" {
		t.Errorf("Next with no device = %v", err)
	}
}

func TestPremiumRequired(t *testing.T) {
	fake := spotifyapitest.New(t)
	useFake(t, fake)
	fake.SetPremium(false)
	c, _ := loggedIn(t, fake)
	err := c.Pause(context.Background(), "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Reason != "PREMIUM_REQUIRED" || !strings.Contains(err.Error(), "Premium") {
		t.Errorf("got %v", err)
	}
}

func TestRefreshOnExpiryAndOn401(t *testing.T) {
	fake := spotifyapitest.New(t)
	useFake(t, fake)
	c, store := loggedIn(t, fake)
	ctx := context.Background()

	// 1. Locally expired token: refreshed before the call, persisted.
	c.mu.Lock()
	c.tokens.Expiry = time.Now().Add(-time.Minute)
	c.mu.Unlock()
	if _, err := c.PlaybackState(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.Issued() != 1 {
		t.Errorf("issued = %d, want 1 refresh", fake.Issued())
	}
	saved, _ := LoadTokens(store)
	if saved.AccessToken != "ACCESS-2" || saved.RefreshToken != "REFRESH-2" {
		t.Errorf("persisted tokens = %+v", saved)
	}

	// 2. Server-side revocation: 401 triggers one refresh and a retry.
	fake.ExpireAccessToken()
	if _, err := c.PlaybackState(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.Issued() != 2 {
		t.Errorf("issued = %d, want 2", fake.Issued())
	}
}

func TestNotLoggedIn(t *testing.T) {
	_, err := NewClient(ClientOptions{ClientID: "x", Store: memStore{}})
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("got %v, want ErrNotLoggedIn", err)
	}
}

func TestRedaction(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer BQAabcdefghijklmnop")
	redactHeader(h, "Authorization")
	if got := h.Get("Authorization"); got != "Bearer BQAa…(redacted)" {
		t.Errorf("redacted = %q", got)
	}
}
