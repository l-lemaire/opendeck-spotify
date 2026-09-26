# opendeck-spotify: Spotify controls for OpenDeck

Control Spotify from Stream Deck keys with
[OpenDeck](https://github.com/nekename/OpenDeck), with the current track on
the play/pause key.

- **Play / Pause** shows the album cover, title, artist, elapsed and total
  time and a progress bar, updated every second. Press to toggle.
- **Next**, **Previous**, and **Loop mode** (off, all, one).
- **Works on this computer and on any device.** With the Spotify desktop app
  running, keys control it directly and follow it instantly. Once connected to
  Spotify's web API, they also control whatever device is playing: phone,
  speaker, another computer.
- **Nothing to install** besides the plugin. Single program, no runtime
  dependencies, for Linux, macOS and Windows.
- **Your Spotify login stays private**: tokens live in your desktop keyring.

## Install

1. Download `opendeck-spotify-<version>.streamDeckPlugin` from the
   [releases page](../../releases).
2. In OpenDeck, open **Settings > Plugins** and install the downloaded file.
   Alternatively unzip it into OpenDeck's plugin folder
   (`~/.config/opendeck/plugins/` on Linux) and restart OpenDeck.
3. Drag the actions from the **Spotify** category onto keys. If the Spotify
   desktop app is running, everything works right away.

## Two ways of controlling Spotify

**Directly, on this computer.** When the Spotify desktop app is running and
playing, the keys talk to it directly through the desktop's media interface.
This needs no account and no setup, reacts instantly, and includes the case
where the app is remote-controlling a phone: the app relays your presses.

**Through Spotify's web API, anywhere.** Connect once (see below) and the keys
also control the device Spotify considers active when the desktop app is not
playing: your phone, a speaker, or nothing running here at all. The
play/pause key then shows a small green ring, and the panel says which device
is being controlled. **Loop mode always uses the web API**, because the
desktop app ignores loop changes sent to it directly.

The rule, in short: the desktop app wins while it plays; otherwise whatever
device Spotify reports as active; otherwise the paused desktop app.

## Connect Spotify (optional, for other devices and loop mode)

Requirements: a **Spotify Premium** account, and a free app registered on
Spotify's developer dashboard:

1. Go to https://developer.spotify.com/dashboard, create an app, and add this
   redirect URI to it: `http://127.0.0.1:8765/callback`. Copy its **Client ID**.
2. In OpenDeck, open any Spotify key's panel, paste the Client ID in the
   **Connect Spotify** section and press the button. Your browser opens
   Spotify's approval page; approve. The panel says "Connected".

The plugin only asks to read what plays and to control playback. Tokens are
stored in your desktop keyring; "Disconnect" removes them, and you can revoke
the app at https://www.spotify.com/account/apps/.

## Display options

The Play / Pause panel lets you choose what the key shows: album cover,
title, artist, time, progress bar, and the text size (Small 14 px to Larger
24 px). Time and progress redraw the key every second while playing; turn
them off for a static key.

## The `spotify-cli` command-line tool

Optional. Download `spotify-cli-<version>-<platform>.tar.gz` (`.zip` on
Windows) from the releases page and unpack it.

```
spotify-cli status                 what plays on this computer (--remote: on any device)
spotify-cli play-pause | next | previous      add --remote to go through the web API
spotify-cli watch                  print changes as the desktop app reports them
spotify-cli auth --client-id <id>  log in to Spotify in the browser (once)
spotify-cli auth status | forget
spotify-cli loop off|all|one       loop mode (web API); --device names a device
spotify-cli devices                your Spotify Connect devices
spotify-cli key --out key.png      render the play/pause key image for a look
spotify-cli plugin status | debug on|off
```

Flags go right after the command. `spotify-cli --debug <command>` shows every
exchange, with tokens redacted.

## Troubleshooting

- **The key says "Spotify not available".** The desktop app is not running
  and either you are not connected to the web API or nothing plays anywhere.
- **"Controlling &lt;phone&gt;" while the app is open.** The app is paused and the
  phone is Spotify's active device; a press resumes the phone, exactly as the
  app's own play button would. Play something in the app to take over.
- **Loop key flashes a warning.** Loop mode needs the web API connection
  (see Connect Spotify), and Spotify Premium.
- **Login fails with "INVALID_CLIENT: Invalid redirect URI".** The redirect URI
  in the dashboard must be exactly `http://127.0.0.1:8765/callback`.
- **Logs.** `spotify-cli plugin status` prints the plugin's log location;
  `spotify-cli plugin debug on` plus an OpenDeck restart records everything,
  including each redraw; turn it off afterwards.

## Privacy and security

- The web API login uses the PKCE flow: no client secret exists, the plugin
  holds only your tokens, in the keyring (GNOME Keyring, KDE Wallet, macOS
  Keychain, Windows Credential Manager), with a file fallback readable only by
  your user.
- Album covers are downloaded from Spotify's image servers and kept in memory
  only. Nothing else leaves your machine besides the API calls you trigger.
- Debug output redacts tokens; everything else is printed verbatim.

## Building from source

Go 1.26 or newer, then `make build` and `make plugin-install`. See
[DEVELOPMENT.md](DEVELOPMENT.md).

## License

MIT, see `LICENSE`.
