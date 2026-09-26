package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/config"
	"github.com/l-lemaire/opendeck-spotify/internal/openaction"
	"github.com/l-lemaire/opendeck-spotify/internal/player"
	"github.com/l-lemaire/opendeck-spotify/internal/spotifyapi"
)

// Panel protocol:
//
//	inspector -> plugin   {"event":"status"}
//	plugin -> inspector   {"event":"status","local":bool,"web_api":bool,"client_id":"...",
//	                       "source":"local|remote|none","device":"...","track":"..."}
//	inspector -> plugin   {"event":"login","client_id":"..."}
//	plugin -> inspector   {"event":"login","stage":"opening|waiting|done|error","message":"..."}
//	inspector -> plugin   {"event":"logout"}   -> a fresh "status"

type inspectorRequest struct {
	Event    string `json:"event"`
	ClientID string `json:"client_id"`
}

type statusReply struct {
	Event    string `json:"event"`
	Local    bool   `json:"local"`
	WebAPI   bool   `json:"web_api"`
	ClientID string `json:"client_id"`
	Source   string `json:"source"`
	Device   string `json:"device,omitempty"`
	Track    string `json:"track,omitempty"`
	Host     string `json:"host"`
}

type loginReply struct {
	Event   string `json:"event"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

func (p *plugin) handleInspectorMessage(ev openaction.Event, payload json.RawMessage) error {
	var req inspectorRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("inspector sent invalid JSON: %w", err)
	}
	switch req.Event {
	case "status":
		return p.sendStatus(ev)
	case "login":
		return p.startLogin(ev, req.ClientID)
	case "logout":
		store, err := p.store()
		if err != nil {
			return err
		}
		store.Delete(spotifyapi.TokensKey)
		p.info.Printf("web API tokens removed from the panel")
		p.startPlayer()
		return p.sendStatus(ev)
	default:
		return fmt.Errorf("inspector sent unknown event %q", req.Event)
	}
}

func (p *plugin) sendStatus(ev openaction.Event) error {
	cfg, _ := config.Load()
	p.mu.Lock()
	st, loggedIn := p.state, p.loggedIn
	p.mu.Unlock()
	reply := statusReply{Event: "status", WebAPI: loggedIn, Source: string(st.Source), Device: st.Device, Track: st.Track.Title, Host: hostname()}
	if cfg != nil {
		reply.ClientID = cfg.ClientID
	}
	// "local" means the desktop client is reachable right now.
	reply.Local = st.Source == player.SourceLocal || p.localRunning()
	return p.conn.SendToPropertyInspector(p.ctx, ev.Action, ev.Context, reply)
}

// localRunning asks the bus whether the client owns its name, using the
// player's own connection.
func (p *plugin) localRunning() bool {
	p.mu.Lock()
	local := p.opts.Local
	p.mu.Unlock()
	if local == nil {
		return false
	}
	running, _ := local.Running()
	return running
}

// startLogin runs the PKCE flow with OpenDeck opening the browser, then
// rebuilds the player so the web API joins.
func (p *plugin) startLogin(ev openaction.Event, clientID string) error {
	if clientID == "" {
		return p.conn.SendToPropertyInspector(p.ctx, ev.Action, ev.Context, loginReply{Event: "login", Stage: "error", Message: "Paste your Spotify app's client id first"})
	}
	if !p.inflight.begin("login") {
		return p.conn.SendToPropertyInspector(p.ctx, ev.Action, ev.Context, loginReply{Event: "login", Stage: "error", Message: "A login is already in progress"})
	}
	cfg, err := config.Load()
	if err != nil {
		p.inflight.end("login")
		return err
	}
	cfg.ClientID = clientID
	if err := cfg.Save(); err != nil {
		p.inflight.end("login")
		return err
	}
	report := func(stage, msg string) {
		p.conn.SendToPropertyInspector(p.ctx, ev.Action, ev.Context, loginReply{Event: "login", Stage: stage, Message: msg})
	}
	go func() {
		defer p.inflight.end("login")
		ctx, cancel := context.WithTimeout(p.ctx, 5*time.Minute)
		defer cancel()
		report("opening", "Opening Spotify's login page in your browser…")
		tokens, err := spotifyapi.Login(ctx, spotifyapi.LoginOptions{
			ClientID: clientID, Log: p.debug,
			OpenBrowser: func(u string) error {
				report("waiting", "Approve the access in your browser, then come back here.")
				return p.conn.OpenURL(ctx, u)
			},
		})
		if err != nil {
			p.info.Printf("login failed: %v", err)
			report("error", err.Error())
			return
		}
		store, err := p.store()
		if err == nil {
			err = spotifyapi.SaveTokens(store, tokens)
		}
		if err != nil {
			report("error", err.Error())
			return
		}
		p.info.Printf("logged in to the web API")
		p.startPlayer()
		report("done", "Connected to Spotify.")
		p.sendStatus(ev)
	}()
	return nil
}
