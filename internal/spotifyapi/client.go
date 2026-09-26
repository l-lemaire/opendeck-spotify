package spotifyapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/l-lemaire/opendeck-spotify/internal/secrets"
)

// Client makes authenticated calls, refreshing the access token when it
// expires and persisting renewed tokens to the store.
type Client struct {
	clientID string
	store    secrets.Store
	http     *http.Client
	log      *log.Logger

	mu     sync.Mutex
	tokens Tokens
}

// ClientOptions is everything NewClient needs.
type ClientOptions struct {
	ClientID string
	// Store holds the tokens; NewClient loads them from it.
	Store secrets.Store
	Log   *log.Logger
	// Timeout caps one request. Default 10 s.
	Timeout time.Duration
}

// ErrNotLoggedIn means no tokens are stored: run the login first.
var ErrNotLoggedIn = errors.New("not logged in to Spotify")

// NewClient loads the stored tokens. It fails with ErrNotLoggedIn (wrapped)
// when none exist.
func NewClient(o ClientOptions) (*Client, error) {
	tokens, err := LoadTokens(o.Store)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return nil, ErrNotLoggedIn
		}
		return nil, err
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		clientID: o.ClientID,
		store:    o.Store,
		tokens:   tokens,
		log:      o.Log,
		http:     &http.Client{Timeout: timeout, Transport: loggingTransport{next: http.DefaultTransport, log: o.Log}},
	}, nil
}

// accessToken returns a valid access token, refreshing first if needed.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.tokens.Expired() {
		return c.tokens.AccessToken, nil
	}
	debugf(c.log, "spotify: access token expired, refreshing")
	renewed, err := Refresh(ctx, c.http, c.clientID, c.tokens)
	if err != nil {
		return "", fmt.Errorf("refresh token: %w", err)
	}
	c.tokens = renewed
	if err := SaveTokens(c.store, renewed); err != nil {
		debugf(c.log, "spotify: could not persist renewed tokens: %v", err)
	}
	return renewed.AccessToken, nil
}

// APIError is a non-2xx answer from the API, with Spotify's reason when
// it gives one ("NO_ACTIVE_DEVICE", "PREMIUM_REQUIRED", ...).
type APIError struct {
	Status  int
	Reason  string
	Message string
	// RetryAfter is set on 429 (rate limited).
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	switch {
	case e.Status == http.StatusTooManyRequests:
		return fmt.Sprintf("spotify rate limit; retry in %s", e.RetryAfter)
	case e.Reason == "NO_ACTIVE_DEVICE":
		return "spotify: no active device (start playing somewhere first)"
	case e.Reason == "PREMIUM_REQUIRED":
		return "spotify: this needs a Premium account"
	case e.Message != "":
		return fmt.Sprintf("spotify: HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("spotify: HTTP %d", e.Status)
}

// do performs one authenticated request against the API. It returns the
// status and body; 2xx is success, anything else becomes an *APIError.
// A 401 triggers one refresh and retry, since a token can be revoked.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (int, []byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.accessToken(ctx)
		if err != nil {
			return 0, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, APIBase+path, body)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set(authHeader, "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return 0, nil, err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			debugf(c.log, "spotify: 401, forcing a token refresh")
			c.mu.Lock()
			c.tokens.Expiry = time.Time{} // marks it expired
			c.mu.Unlock()
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp.StatusCode, data, nil
		}
		return resp.StatusCode, data, apiError(resp, data)
	}
	return 0, nil, errors.New("spotify: unauthorized after refresh; log in again")
}

func apiError(resp *http.Response, body []byte) *APIError {
	e := &APIError{Status: resp.StatusCode}
	var wrapper struct {
		Error struct {
			Message string `json:"message"`
			Reason  string `json:"reason"`
		} `json:"error"`
	}
	if err := unmarshal(body, &wrapper); err == nil {
		e.Message, e.Reason = wrapper.Error.Message, wrapper.Error.Reason
	}
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			e.RetryAfter = time.Duration(n) * time.Second
		}
	}
	return e
}
