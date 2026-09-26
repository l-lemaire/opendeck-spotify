package spotifyapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Repeat modes as the web API names them. MPRIS calls them None/Track/Playlist.
const (
	RepeatOff     = "off"
	RepeatTrack   = "track"
	RepeatContext = "context"
)

// Device is one of the user's Spotify Connect devices.
type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // Computer, Smartphone, Speaker, ...
	IsActive bool   `json:"is_active"`
	Volume   int    `json:"volume_percent"`
}

// Track is the playing item, reduced to what the keys show.
type Track struct {
	ID       string
	Title    string
	Artist   string
	Album    string
	ArtURL   string // largest cover image
	URL      string
	Duration time.Duration
}

// State is the user's playback across all devices.
type State struct {
	// Active is false when nothing is playing anywhere (the API answers
	// 204 No Content); the other fields are then empty.
	Active   bool
	Playing  bool
	Repeat   string // off, track, context
	Shuffle  bool
	Progress time.Duration
	Track    Track
	Device   Device
	At       time.Time
}

// PlaybackState fetches GET /me/player.
func (c *Client) PlaybackState(ctx context.Context) (State, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/me/player", nil)
	if err != nil {
		return State{}, err
	}
	if status == http.StatusNoContent || len(body) == 0 {
		return State{At: time.Now()}, nil
	}
	var raw struct {
		Device      Device `json:"device"`
		RepeatState string `json:"repeat_state"`
		Shuffle     bool   `json:"shuffle_state"`
		IsPlaying   bool   `json:"is_playing"`
		ProgressMS  int64  `json:"progress_ms"`
		Item        *struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			DurationMS int64  `json:"duration_ms"`
			Artists    []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Album struct {
				Name   string `json:"name"`
				Images []struct {
					URL    string `json:"url"`
					Width  int    `json:"width"`
					Height int    `json:"height"`
				} `json:"images"`
			} `json:"album"`
			ExternalURLs struct {
				Spotify string `json:"spotify"`
			} `json:"external_urls"`
		} `json:"item"`
	}
	if err := unmarshal(body, &raw); err != nil {
		return State{}, fmt.Errorf("GET /me/player: bad JSON: %w", err)
	}
	st := State{
		Active: true, Playing: raw.IsPlaying, Repeat: raw.RepeatState, Shuffle: raw.Shuffle,
		Progress: time.Duration(raw.ProgressMS) * time.Millisecond, Device: raw.Device, At: time.Now(),
	}
	if it := raw.Item; it != nil {
		st.Track = Track{ID: it.ID, Title: it.Name, Album: it.Album.Name, URL: it.ExternalURLs.Spotify,
			Duration: time.Duration(it.DurationMS) * time.Millisecond}
		var names []string
		for _, a := range it.Artists {
			names = append(names, a.Name)
		}
		st.Track.Artist = strings.Join(names, ", ")
		best := 0
		for _, img := range it.Album.Images {
			if img.Width >= best {
				best, st.Track.ArtURL = img.Width, img.URL
			}
		}
	}
	return st, nil
}

// Devices lists the user's Spotify Connect devices.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	_, body, err := c.do(ctx, http.MethodGet, "/me/player/devices", nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Devices []Device `json:"devices"`
	}
	if err := unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("GET /me/player/devices: bad JSON: %w", err)
	}
	return raw.Devices, nil
}

// Transport controls. deviceID may be empty to target the active device.
// They all need a Premium account.

func (c *Client) Play(ctx context.Context, deviceID string) error {
	_, _, err := c.do(ctx, http.MethodPut, "/me/player/play"+deviceQuery(deviceID, nil), nil)
	return err
}

func (c *Client) Pause(ctx context.Context, deviceID string) error {
	_, _, err := c.do(ctx, http.MethodPut, "/me/player/pause"+deviceQuery(deviceID, nil), nil)
	return err
}

func (c *Client) Next(ctx context.Context, deviceID string) error {
	_, _, err := c.do(ctx, http.MethodPost, "/me/player/next"+deviceQuery(deviceID, nil), nil)
	return err
}

func (c *Client) Previous(ctx context.Context, deviceID string) error {
	_, _, err := c.do(ctx, http.MethodPost, "/me/player/previous"+deviceQuery(deviceID, nil), nil)
	return err
}

// SetRepeat sets the loop mode: RepeatOff, RepeatTrack or RepeatContext.
func (c *Client) SetRepeat(ctx context.Context, mode, deviceID string) error {
	_, _, err := c.do(ctx, http.MethodPut, "/me/player/repeat"+deviceQuery(deviceID, url.Values{"state": {mode}}), nil)
	return err
}

// PlayPause reads the state and flips it. Two requests; the API has no toggle.
func (c *Client) PlayPause(ctx context.Context) (playing bool, err error) {
	st, err := c.PlaybackState(ctx)
	if err != nil {
		return false, err
	}
	if st.Playing {
		return false, c.Pause(ctx, "")
	}
	return true, c.Play(ctx, "")
}

func deviceQuery(deviceID string, extra url.Values) string {
	q := url.Values{}
	for k, v := range extra {
		q[k] = v
	}
	if deviceID != "" {
		q.Set("device_id", deviceID)
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

func unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return fmt.Errorf("empty body")
	}
	return json.Unmarshal(data, v)
}
