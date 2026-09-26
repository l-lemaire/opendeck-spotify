// Package mpris talks to the Spotify desktop client through MPRIS, the
// standard interface Linux media players expose on the user's session
// D-Bus (Media Player Remote Interfacing Specification, version 2).
//
// The client registers the bus name "org.mpris.MediaPlayer2.spotify" and one
// object, "/org/mpris/MediaPlayer2", whose "org.mpris.MediaPlayer2.Player"
// interface offers:
//
//	methods     PlayPause, Next, Previous, Play, Pause, Seek, SetPosition
//	properties  PlaybackStatus (Playing/Paused/Stopped), LoopStatus
//	            (None/Track/Playlist), Shuffle, Position (microseconds),
//	            Metadata (title, artist, album, art URL, length, ...)
//	signals     PropertiesChanged when state or track changes; Seeked on jumps
//
// Two Spotify quirks, both verified on the Flatpak client 1.2.95:
//
//   - Position is not pushed: it is a value to read. The client announces
//     seeks but not the running clock, so callers advance the position
//     themselves while playing and re-read it occasionally (see player).
//   - LoopStatus and Shuffle can be read but writes are ignored: Spotify
//     acknowledges the change and keeps its value. Changing the loop mode
//     therefore goes through the web API (package spotifyapi).
//
// This package needs no Spotify account and no network. It is the fast
// path used whenever the desktop client is the active player.
package mpris
