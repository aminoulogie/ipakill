# ipakill

Sign and install `.ipa` files on an iPhone with a free Apple ID, from the
Windows command line or from the ipakill iPhone app over Wi-Fi.

- `pc/` – `ipakill-core.exe` (Go): runs plumesign, tracks the 7-day expiry,
  serves the iPhone app on port 7777.
- `ios/` – the ipakill iPhone app. It is built on
  [LiveContainer](https://github.com/LiveContainer/LiveContainer)'s engine, so
  apps can run *inside* ipakill: no install slot, no app ID, nothing to re-sign
  but ipakill itself. GitHub Actions checks out LiveContainer at a pinned
  commit, `ios/patch_lc.py` adds ipakill's tabs (Sources, Library, Activity)
  and branding, and the unsigned .ipa is published as a release.
- The `ipakill` command itself is `~/.local/bin/ipakill.cmd` + `ipakill.ps1`.

## Build the PC part

```
cd pc
go build -o %USERPROFILE%\.local\bin\ipakill-core.exe .
```

## Use

```
ipakill login            once
ipakill app.ipa          sign + install (USB, or Wi-Fi if enabled in iTunes)
ipakill list             days left per app
ipakill serve            keep running so the iPhone app can sync
```

Wi-Fi installs need "Sync with this iPhone over Wi-Fi" turned on in iTunes
(iPhone connected by USB once), and the PC and iPhone on the same network.

## Terminal (run PC commands from the phone)

Start the server with the terminal allowed:

```
ipakill-core serve --shell
```

Activity → Terminal then runs commands on the PC (cmd.exe), one at a time.
Built-ins: `cd <dir>` (remembered), `update` (download the latest prebuilt
ipakill-core.exe from the `pc-core` release and restart), `restart`. It is only as safe as the
pairing code: ten wrong codes lock the server for five minutes.

## Apps inside ipakill

From iOS 26 on, apps run inside ipakill must be signed on the phone with the
same certificate as ipakill. The PC keeps that key (plumesign stores it under
`%APPDATA%\PlumeImpactor\keys`); Library → Certificate → IMPORT fetches it
over the paired Wi-Fi link. Only run .ipa files you trust inside ipakill: they
share its storage, including that certificate.

## License

AGPL-3.0, because the iPhone app includes LiveContainer (AGPL-3.0).
