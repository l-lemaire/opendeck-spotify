// Package spotifyapitest is a fake of the parts of Spotify's accounts and
// web API this project uses: PKCE token exchange and refresh, playback
// state, transport controls, repeat mode, devices.
package spotifyapitest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Server is the fake.
type Server struct {
	*httptest.Server
	ClientID string
	// Codes accepted by the token exchange, mapped to the verifier they
	// expect (the fake does not hash; it compares what the client sends).
	mu           sync.Mutex
	accessToken  string
	refreshToken string
	issued       int      // how many token exchanges/refreshes happened
	calls        []string // "PUT /me/player/pause" etc.
	premium      bool

	// Playback state served by GET /me/player.
	Active   bool
	Playing  bool
	Repeat   string
	Progress int64
	Device   string // active device name
}

// New starts the fake with one active playing track on a "Living room" speaker.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{ClientID: "fake-client-id", accessToken: "ACCESS-1", refreshToken: "REFRESH-1", premium: true,
		Active: true, Playing: true, Repeat: "off", Progress: 42_000, Device: "Living room"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/token", s.token)
	mux.HandleFunc("GET /v1/me/player", s.authed(s.player))
	mux.HandleFunc("GET /v1/me/player/devices", s.authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"devices": []map[string]any{
			{"id": "dev-1", "name": s.Device, "type": "Speaker", "is_active": s.Active, "volume_percent": 40},
			{"id": "dev-2", "name": "This computer", "type": "Computer", "is_active": false, "volume_percent": 100},
		}})
	}))
	for _, ep := range []string{"PUT /v1/me/player/play", "PUT /v1/me/player/pause", "POST /v1/me/player/next", "POST /v1/me/player/previous", "PUT /v1/me/player/repeat"} {
		mux.HandleFunc(ep, s.authed(s.control))
	}
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Server.Close)
	return s
}

// Calls returns the control endpoints hit so far, e.g. "PUT /me/player/pause?state=".
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// Issued returns how many token exchanges or refreshes happened.
func (s *Server) Issued() int { s.mu.Lock(); defer s.mu.Unlock(); return s.issued }

// ExpireAccessToken makes the current access token invalid, so the next
// call gets 401 and the client must refresh.
func (s *Server) ExpireAccessToken() { s.mu.Lock(); s.accessToken = "EXPIRED"; s.mu.Unlock() }

// SetPremium controls whether control endpoints answer 403 PREMIUM_REQUIRED.
func (s *Server) SetPremium(p bool) { s.mu.Lock(); s.premium = p; s.mu.Unlock() }

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Form.Get("client_id") != s.ClientID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": "Invalid client"})
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		if r.Form.Get("code") != "GOODCODE" || r.Form.Get("code_verifier") == "" || r.Form.Get("redirect_uri") == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "Invalid authorization code"})
			return
		}
	case "refresh_token":
		if r.Form.Get("refresh_token") != s.refreshToken {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "Refresh token revoked"})
			return
		}
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.issued++
	s.accessToken = "ACCESS-" + itoa(s.issued+1)
	s.refreshToken = "REFRESH-" + itoa(s.issued+1)
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": s.accessToken, "refresh_token": s.refreshToken, "expires_in": 3600, "token_type": "Bearer",
		"scope": "user-read-playback-state user-modify-playback-state user-read-currently-playing",
	})
}

// authed checks the bearer token like the real API.
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := r.Header.Get("Authorization") == "Bearer "+s.accessToken
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"status": 401, "message": "The access token expired"}})
			return
		}
		next(w, r)
	}
}

func (s *Server) player(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Active {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"device":       map[string]any{"id": "dev-1", "name": s.Device, "type": "Speaker", "is_active": true, "volume_percent": 40},
		"repeat_state": s.Repeat, "shuffle_state": false, "is_playing": s.Playing, "progress_ms": s.Progress,
		"currently_playing_type": "track",
		"item": map[string]any{
			"id": "2g2a5kDeZexbUTD8abcvm6", "name": "Lifestyles of the Rich & Famous", "duration_ms": 190466,
			"artists": []map[string]any{{"name": "Good Charlotte"}},
			"album": map[string]any{"name": "The Young and The Hopeless", "images": []map[string]any{
				{"url": "https://i.scdn.co/image/small", "width": 64, "height": 64},
				{"url": "https://i.scdn.co/image/large", "width": 640, "height": 640},
				{"url": "https://i.scdn.co/image/medium", "width": 300, "height": 300},
			}},
			"external_urls": map[string]any{"spotify": "https://open.spotify.com/track/2g2a5kDeZexbUTD8abcvm6"},
		},
	})
}

func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.premium {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"status": 403, "message": "Player command failed: Premium required", "reason": "PREMIUM_REQUIRED"}})
		return
	}
	if !s.Active {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"status": 404, "message": "Player command failed: No active device found", "reason": "NO_ACTIVE_DEVICE"}})
		return
	}
	s.calls = append(s.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	switch r.URL.Path {
	case "/v1/me/player/play":
		s.Playing = true
	case "/v1/me/player/pause":
		s.Playing = false
	case "/v1/me/player/repeat":
		s.Repeat = r.URL.Query().Get("state")
	}
	w.WriteHeader(http.StatusNoContent)
}

func itoa(n int) string { return jsonInt(n) }

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
