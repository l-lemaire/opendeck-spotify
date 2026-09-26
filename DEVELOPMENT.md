# Development notes

Sister project of [opendeck-hue](https://github.com/l-lemaire/opendeck-hue) and
[opendeck-nanoleaf](https://github.com/l-lemaire/opendeck-nanoleaf), built the
same way; `internal/openaction`, `internal/secrets` and `internal/config` are
adapted copies.

## Local control: MPRIS

The Spotify desktop client (Flatpak `com.spotify.Client`, tested with 1.2.95)
exposes `org.mpris.MediaPlayer2.spotify` on the session bus. See
`internal/mpris/doc.go` for what works and the two quirks: position is read,
not pushed, and loop/shuffle writes are ignored by Spotify.

```
make build          # bin/spotify-cli
make check          # gofmt, go vet, tests (the MPRIS tests need a session bus; they skip without one)
```

## Remote control: Spotify Web API

`internal/spotifyapi`: PKCE login through a callback on `http://127.0.0.1:8765/callback`
(the redirect URI registered in the developer dashboard; `localhost` is not
accepted by Spotify), tokens in the keyring under `opendeck-spotify`, automatic
refresh with rotation, `GET /me/player`, transport controls, repeat mode,
devices. Control endpoints need Premium; development-mode apps require the
owner to have Premium. Tests run against `spotifyapitest`, a fake of the
accounts and API endpoints.

Verified on the real account: after the local client has been paused for a
while Spotify reports no active device and refuses commands with
`NO_ACTIVE_DEVICE`; naming the device (`device_id`) makes them succeed and
makes the device active again. The local client appears in the device list
as a "Computer" named after the host.

## Merged model and rendering

`internal/player` merges both sources under one state with the rule in its
package comment (local playing wins; otherwise music elsewhere; otherwise the
paused local client). The running clock is computed locally from the last
read position (`State.PositionAt`). `internal/render` draws the 144 px key
with the Go fonts bundled in `golang.org/x/image`; `internal/art` caches
decoded covers. `spotify-cli key --out file.png` renders the live key for a
visual check.

## Plugin

`cmd/opendeck-spotify`: the merged player runs inside the plugin; keys redraw
on every state change and, while playing, once a second for keys showing time
or progress. The panel's status is pushed on state changes. The web API login
from the panel uses OpenDeck's `openUrl` to open the browser. Tests run the
plugin between a fake OpenDeck, a fake MPRIS player on the session bus and a
fake Spotify API.

## Release

```
git tag -a vX.Y.Z -m "..."
make plugin-release     # dist/opendeck-spotify-X.Y.Z.streamDeckPlugin + spotify-cli-X.Y.Z-<triple> archives
```
