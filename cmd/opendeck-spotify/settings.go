package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/l-lemaire/opendeck-spotify/internal/render"
)

const actionPrefix = "com.github.l-lemaire.spotify."

// The four actions declared in plugin/manifest.json.
const (
	actionPlayPause = actionPrefix + "play-pause"
	actionNext      = actionPrefix + "next"
	actionPrevious  = actionPrefix + "previous"
	actionLoop      = actionPrefix + "loop"
)

// Settings is what a key remembers. Only the play/pause key has settings:
// its display options. Pointers to bool let "absent" mean "default on".
type Settings struct {
	ShowArt      *bool   `json:"show_art,omitempty"`
	ShowTitle    *bool   `json:"show_title,omitempty"`
	ShowArtist   *bool   `json:"show_artist,omitempty"`
	ShowTime     *bool   `json:"show_time,omitempty"`
	ShowProgress *bool   `json:"show_progress,omitempty"`
	TextScale    float64 `json:"text_scale,omitempty"`
}

// renderOptions converts the settings to what the renderer wants.
func (s Settings) renderOptions() render.Options {
	on := func(b *bool) bool { return b == nil || *b }
	o := render.Options{
		ShowArt: on(s.ShowArt), ShowTitle: on(s.ShowTitle), ShowArtist: on(s.ShowArtist),
		ShowTime: on(s.ShowTime), ShowProgress: on(s.ShowProgress), TextScale: s.TextScale,
	}
	if o.TextScale <= 0 {
		o.TextScale = 1
	}
	return o
}

// needsTicker reports whether the key shows something that moves every
// second while playing.
func (s Settings) needsTicker() bool {
	o := s.renderOptions()
	return o.ShowTime || o.ShowProgress
}

func decodeSettings(raw json.RawMessage) (Settings, error) {
	var s Settings
	if len(raw) == 0 || string(raw) == "null" {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("decode settings: %w", err)
	}
	return s, nil
}

func checkAction(uuid string) error {
	switch uuid {
	case actionPlayPause, actionNext, actionPrevious, actionLoop:
		return nil
	}
	return fmt.Errorf("unknown action %q", uuid)
}

func shortAction(uuid string) string { return strings.TrimPrefix(uuid, actionPrefix) }
