package spotifyapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/secrets"
)

// Endpoints. Variables rather than constants so tests can point them at a
// fake server.
var (
	AuthorizeURL = "https://accounts.spotify.com/authorize"
	TokenURL     = "https://accounts.spotify.com/api/token"
	APIBase      = "https://api.spotify.com/v1"
)

// DefaultPort is where the login callback listens. The redirect URI
// registered in the Spotify developer dashboard must match:
// http://127.0.0.1:8765/callback
const DefaultPort = 8765

// Scopes are the permissions requested: read what plays, change playback,
// read the currently playing item.
var Scopes = []string{"user-read-playback-state", "user-modify-playback-state", "user-read-currently-playing"}

// TokensKey is the keyring entry name for the stored tokens.
const TokensKey = "spotify-tokens"

// Tokens is what a login produces and a refresh renews.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	Scope        string    `json:"scope"`
}

// Expired reports whether the access token needs a refresh, with a margin
// so a request started just before expiry does not fail.
func (t Tokens) Expired() bool {
	return time.Now().Add(30 * time.Second).After(t.Expiry)
}

// SaveTokens / LoadTokens persist the tokens as one JSON entry.
func SaveTokens(store secrets.Store, t Tokens) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return store.Set(TokensKey, string(raw))
}

func LoadTokens(store secrets.Store) (Tokens, error) {
	raw, err := store.Get(TokensKey)
	if err != nil {
		return Tokens{}, fmt.Errorf("spotify tokens: %w", err)
	}
	var t Tokens
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return Tokens{}, fmt.Errorf("spotify tokens are corrupt: %w", err)
	}
	return t, nil
}

// LoginOptions configures Login.
type LoginOptions struct {
	ClientID string
	// Port for the callback server. Zero means DefaultPort.
	Port int
	// OpenBrowser receives the authorization URL. nil means xdg-open; a
	// caller that cannot open a browser (the plugin asks OpenDeck instead)
	// or a test supplies its own.
	OpenBrowser func(url string) error
	// Timeout for the user to finish in the browser. Zero means 5 minutes.
	Timeout time.Duration
	Log     *log.Logger
	// HTTPClient for the token exchange; nil means a default with the debug
	// logger attached.
	HTTPClient *http.Client
}

// Login runs the PKCE flow and returns the tokens. It blocks until the
// browser comes back to the callback, the timeout passes, or ctx ends.
func Login(ctx context.Context, o LoginOptions) (Tokens, error) {
	if o.ClientID == "" {
		return Tokens{}, errors.New("a Spotify client id is required (create an app at developer.spotify.com/dashboard)")
	}
	port := o.Port
	if port == 0 {
		port = DefaultPort
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	open := o.OpenBrowser
	if open == nil {
		open = xdgOpen
	}

	verifier, err := randomString(64)
	if err != nil {
		return Tokens{}, err
	}
	state, err := randomString(16)
	if err != nil {
		return Tokens{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// The callback server: one request expected, carrying code and state.
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return Tokens{}, fmt.Errorf("cannot listen on 127.0.0.1:%d for the login callback: %w", port, err)
	}
	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		switch {
		case q.Get("state") != state:
			http.Error(w, "state mismatch", http.StatusBadRequest)
			results <- result{err: errors.New("login callback with wrong state; try again")}
		case q.Get("error") != "":
			fmt.Fprintf(w, "<h2>Spotify login failed: %s</h2>You can close this tab.", q.Get("error"))
			results <- result{err: fmt.Errorf("spotify refused the login: %s", q.Get("error"))}
		default:
			fmt.Fprint(w, "<h2>Spotify login done.</h2>You can close this tab and go back to OpenDeck.")
			results <- result{code: q.Get("code")}
		}
	})}
	go srv.Serve(listener)
	defer srv.Close()

	params := url.Values{
		"client_id":             {o.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirect},
		"code_challenge_method": {"S256"},
		"code_challenge":        {challenge},
		"state":                 {state},
		"scope":                 {strings.Join(Scopes, " ")},
	}
	authURL := AuthorizeURL + "?" + params.Encode()
	debugf(o.Log, "spotify: opening %s", authURL)
	if err := open(authURL); err != nil {
		return Tokens{}, fmt.Errorf("open browser: %w", err)
	}

	var code string
	select {
	case r := <-results:
		if r.err != nil {
			return Tokens{}, r.err
		}
		code = r.code
	case <-time.After(timeout):
		return Tokens{}, fmt.Errorf("no login within %s", timeout)
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	}

	client := o.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, Transport: loggingTransport{next: http.DefaultTransport, log: o.Log}}
	}
	return exchange(ctx, client, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"client_id":     {o.ClientID},
		"code_verifier": {verifier},
	})
}

// Refresh obtains a new access token (and usually a new refresh token).
func Refresh(ctx context.Context, client *http.Client, clientID string, t Tokens) (Tokens, error) {
	if t.RefreshToken == "" {
		return Tokens{}, errors.New("no refresh token; log in again")
	}
	renewed, err := exchange(ctx, client, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
		"client_id":     {clientID},
	})
	if err != nil {
		return Tokens{}, err
	}
	if renewed.RefreshToken == "" {
		renewed.RefreshToken = t.RefreshToken // Spotify may omit it when unchanged
	}
	return renewed, nil
}

// exchange posts to the token endpoint and decodes the reply.
func exchange(ctx context.Context, client *http.Client, form url.Values) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		json.Unmarshal(body, &e)
		if e.Error != "" {
			return Tokens{}, fmt.Errorf("spotify token endpoint: %s: %s", e.Error, e.Description)
		}
		return Tokens{}, fmt.Errorf("spotify token endpoint: HTTP %d", resp.StatusCode)
	}
	var reply struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &reply); err != nil || reply.AccessToken == "" {
		return Tokens{}, errors.New("spotify token endpoint: unexpected response")
	}
	return Tokens{
		AccessToken:  reply.AccessToken,
		RefreshToken: reply.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(reply.ExpiresIn) * time.Second),
		Scope:        reply.Scope,
	}, nil
}

// randomString returns n bytes of randomness encoded URL-safe.
func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n], nil
}

// xdgOpen opens a URL in the user's default browser on Linux.
func xdgOpen(u string) error {
	return exec.Command("xdg-open", u).Start()
}

func debugf(l *log.Logger, format string, args ...any) {
	if l != nil {
		l.Printf(format, args...)
	}
}
