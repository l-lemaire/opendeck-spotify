// Package spotifyapi talks to Spotify's Web API for what the desktop client
// cannot do locally: control playback on other devices, and change the loop
// mode (which Spotify ignores over MPRIS).
//
// Access requires a Spotify developer application (its client id goes in
// the config) and a one-time login in the browser using the PKCE flow:
//
//  1. Login starts a tiny HTTP server on 127.0.0.1:<port>/callback and opens
//     https://accounts.spotify.com/authorize in the browser with a random
//     "code challenge" (the hash of a random verifier).
//  2. The user approves; Spotify redirects the browser to the callback with
//     an authorization code.
//  3. The code plus the original verifier are exchanged for an access token
//     (valid one hour) and a refresh token. No client secret is involved,
//     which is the point of PKCE: nothing to hide in the binary.
//
// Tokens live in the keyring (package secrets). The access token is
// refreshed automatically; Spotify rotates refresh tokens, so every refresh
// is persisted.
//
// Files:
//
//	auth.go      PKCE login, token exchange and refresh, token storage
//	client.go    authenticated HTTP client with automatic refresh
//	player.go    playback state, transport controls, repeat mode, devices
//	httplog.go   debug dumps with tokens redacted
package spotifyapi
