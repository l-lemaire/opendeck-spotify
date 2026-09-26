package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/l-lemaire/opendeck-spotify/internal/mpris"
	"github.com/l-lemaire/opendeck-spotify/internal/mpris/mpristest"
	"github.com/l-lemaire/opendeck-spotify/internal/openaction"
	"github.com/l-lemaire/opendeck-spotify/internal/player"
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

type harness struct {
	host  *websocket.Conn
	sent  chan map[string]any
	mp    *mpristest.Player
	api   *spotifyapitest.Server
	local *mpris.Player
}

// start wires a plugin between a fake OpenDeck and fake Spotify sources.
func start(t *testing.T, withAPI bool) *harness {
	t.Helper()
	h := &harness{mp: mpristest.New(t)}
	local, err := mpris.Connect(h.mp.BusName, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	h.local = local

	var remote *spotifyapi.Client
	if withAPI {
		h.api = spotifyapitest.New(t)
		h.api.Active = false // nothing plays elsewhere; the local client is in charge
		oldAuth, oldToken, oldAPI := spotifyapi.AuthorizeURL, spotifyapi.TokenURL, spotifyapi.APIBase
		spotifyapi.AuthorizeURL, spotifyapi.TokenURL, spotifyapi.APIBase = h.api.URL+"/authorize", h.api.URL+"/api/token", h.api.URL+"/v1"
		t.Cleanup(func() { spotifyapi.AuthorizeURL, spotifyapi.TokenURL, spotifyapi.APIBase = oldAuth, oldToken, oldAPI })
		store := memStore{}
		spotifyapi.SaveTokens(store, spotifyapi.Tokens{AccessToken: "ACCESS-1", RefreshToken: "REFRESH-1", Expiry: time.Now().Add(time.Hour)})
		remote, err = spotifyapi.NewClient(spotifyapi.ClientOptions{ClientID: h.api.ClientID, Store: store})
		if err != nil {
			t.Fatal(err)
		}
	}

	hostConn := make(chan *websocket.Conn, 1)
	h.sent = make(chan map[string]any, 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		hostConn <- c
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			h.sent <- m
		}
	}))
	t.Cleanup(srv.Close)
	port, _ := strconv.Atoi(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var debug *log.Logger
	if testing.Verbose() {
		debug = log.New(os.Stderr, "    debug: ", 0)
	}
	conn, err := openaction.Connect(ctx, openaction.Args{Port: port, PluginUUID: "com.github.l-lemaire.spotify.sdPlugin", RegisterEvent: "registerPlugin"}, debug)
	if err != nil {
		t.Fatal(err)
	}
	p := newPluginWith(ctx, conn, log.New(io.Discard, "", 0), debug, func() player.Options {
		return player.Options{Local: local, Remote: remote, LocalDeviceName: "This computer", RemotePoll: 100 * time.Millisecond, RemoteIdlePoll: 100 * time.Millisecond}
	})
	go conn.Run(ctx, p.handlers())
	h.host = <-hostConn
	if reg := h.next(t); reg["event"] != "registerPlugin" {
		t.Fatalf("first message = %v", reg)
	}
	// Wait until the plugin has taken the local client in charge, so key
	// presses in the tests are not lost to "nothing to control".
	deadline := time.Now().Add(5 * time.Second)
	for p.State().Source != player.SourceLocal {
		if time.Now().After(deadline) {
			t.Fatal("plugin never saw the local client")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return h
}

// call waits for the next control call on the fake client.
func (h *harness) call(t *testing.T) string {
	t.Helper()
	select {
	case c := <-h.mp.Calls:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("no call reached the client within 3s")
		return ""
	}
}

func (h *harness) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case m := <-h.sent:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("plugin sent nothing within 5s")
		return nil
	}
}

// expect drains until a message with the event (and context, if given).
func (h *harness) expect(t *testing.T, event, context string) map[string]any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-h.sent:
			if m["event"] == event && (context == "" || m["context"] == context) {
				return m
			}
		case <-deadline:
			t.Fatalf("no %s for %q within 5s", event, context)
			return nil
		}
	}
}

func (h *harness) push(t *testing.T, v any) {
	t.Helper()
	data, _ := json.Marshal(v)
	if err := h.host.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func appear(action, ctx string, settings map[string]any) map[string]any {
	return map[string]any{"event": "willAppear", "action": action, "context": ctx, "payload": map[string]any{"settings": settings, "controller": "Keypad"}}
}
func keyDown(action, ctx string) map[string]any {
	return map[string]any{"event": "keyDown", "action": action, "context": ctx, "payload": map[string]any{"settings": map[string]any{}, "state": 0}}
}

func TestPlayPauseKeyDrawsAndToggles(t *testing.T) {
	h := start(t, false)
	h.push(t, appear(actionPlayPause, "pp", map[string]any{"show_art": false}))
	m := h.expect(t, "setImage", "pp")
	img := m["payload"].(map[string]any)["image"].(string)
	if !strings.HasPrefix(img, "data:image/png;base64,") || len(img) < 500 {
		t.Errorf("image = %.40s (%d chars)", img, len(img))
	}

	h.push(t, keyDown(actionPlayPause, "pp"))
	if call := h.call(t); call != "PlayPause" {
		t.Errorf("call = %s", call)
	}
	// The client is now playing; the key is redrawn from the change.
	h.expect(t, "setImage", "pp")
}

func TestNextPreviousKeys(t *testing.T) {
	h := start(t, false)
	h.push(t, keyDown(actionNext, "n"))
	if call := h.call(t); call != "Next" {
		t.Errorf("call = %s", call)
	}
	h.expect(t, "showOk", "n")
	h.push(t, keyDown(actionPrevious, "pv"))
	if call := h.call(t); call != "Previous" {
		t.Errorf("call = %s", call)
	}
}

func TestLoopKeyNeedsAPIAndCycles(t *testing.T) {
	// Without the API: the key shows the mode but a press alerts.
	h := start(t, false)
	h.push(t, appear(actionLoop, "lp", nil))
	if st := h.expect(t, "setState", "lp"); int(st["payload"].(map[string]any)["state"].(float64)) != 1 { // Playlist = all
		t.Errorf("loop state = %v, want 1 (all)", st)
	}
	h.push(t, keyDown(actionLoop, "lp"))
	h.expect(t, "showAlert", "lp")

	// With the API: the press cycles all -> one through the API, naming
	// this computer's device since the local client is in charge.
	h2 := start(t, true)
	h2.push(t, appear(actionLoop, "lp2", nil))
	h2.expect(t, "setState", "lp2")
	h2.push(t, keyDown(actionLoop, "lp2"))
	deadline := time.After(3 * time.Second)
	for {
		calls := h2.api.Calls()
		if len(calls) > 0 {
			if !strings.Contains(calls[0], "repeat") || !strings.Contains(calls[0], "state=track") || !strings.Contains(calls[0], "device_id=dev-2") {
				t.Errorf("api call = %s", calls[0])
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("no API call")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestInspectorStatus(t *testing.T) {
	h := start(t, true)
	h.push(t, map[string]any{"event": "sendToPlugin", "action": actionPlayPause, "context": "pp", "payload": map[string]any{"event": "status"}})
	m := h.expect(t, "sendToPropertyInspector", "pp")
	payload := m["payload"].(map[string]any)
	if payload["event"] != "status" || payload["local"] != true || payload["web_api"] != true || payload["source"] != "local" {
		t.Errorf("status = %v", payload)
	}
}

func TestSettingsHelpers(t *testing.T) {
	s, err := decodeSettings(json.RawMessage(`{"show_time":false,"text_scale":1.2}`))
	if err != nil {
		t.Fatal(err)
	}
	o := s.renderOptions()
	if !o.ShowArt || o.ShowTime || o.TextScale != 1.2 || !s.needsTicker() {
		t.Errorf("options = %+v ticker=%v", o, s.needsTicker())
	}
	quiet, _ := decodeSettings(json.RawMessage(`{"show_time":false,"show_progress":false}`))
	if quiet.needsTicker() {
		t.Error("no time and no progress should not tick")
	}
	if err := checkAction("com.other"); err == nil {
		t.Error("foreign action accepted")
	}
}
