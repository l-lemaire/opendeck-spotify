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
