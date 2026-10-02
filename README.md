# ipakill

Sign and install `.ipa` files on an iPhone with a free Apple ID, from the
Windows command line or from the ipakill iPhone app over Wi-Fi.

- `pc/` – `ipakill-core.exe` (Go): runs plumesign, tracks the 7-day expiry,
  serves the iPhone app on port 7777.
- `ios/` – the ipakill iPhone app (SwiftUI, ESign-style tabs: Sources, Library, Activity, Settings). Built unsigned by
  GitHub Actions; download the `ipakill-ipa` artifact and run `ipakill ipakill.ipa`.
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
