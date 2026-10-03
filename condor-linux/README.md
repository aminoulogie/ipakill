# condor-linux

Our own Linux on a **Condor TRA-901G** tablet (Intel Atom Z2580 "Clover Trail+", 2 GB RAM,
8.9" 1920×1200, Android 4.2.2). It boots straight into Alpine Linux 3.24 and our Go
`condor-init`, with a console on the screen, touch, Wi-Fi and SSH, on top of Condor's own
signed kernel (which already has the drivers). Android stays installed underneath.

- How it works, and the daily commands: [`CLAUDE.md`](CLAUDE.md)
- What's done and what's next: [`PLAN.md`](PLAN.md)
- Everything learned about the hardware, with sources: [`docs/KNOWLEDGE.md`](docs/KNOWLEDGE.md)

## Layout

| Path | What |
|---|---|
| `cli/` | `condor.exe`, the Windows command-line tool that drives the tablet over USB (adb) |
| `os/condor-init/` | the program that runs on the tablet instead of Android's UI (Go, linux/386) |
| `os/condor-init/vt/` | the terminal engine behind the on-screen console |
| `os/condor-init/ui/` | text rendering with the Go fonts, and a simple launcher (fallback) |
| `dev.cmd`, `dev.ps1` | build `condor-init` and run it on the tablet in one step |
| `recon.sh` | the original read-only inspection script (bash); `condor recon` replaced it |
| `docs/` | knowledge base and the original handoff prompt |

## Build

```
cd cli
go build -o condor.exe .
.\condor.exe setup          # puts 'condor' on PATH; downloads adb on first use
cd ..
.\dev.cmd                   # builds os/condor-init for linux/386 and installs it on the tablet
```

## Commands

```
condor doctor                          check adb, cable, and authorization
condor info                            model, Android, kernel, battery, storage
condor term                            the tablet's console from the PC (Ctrl+] leaves)
condor ssh setup | condor ssh          log in over Wi-Fi (setup once, over USB)
condor net                             internet for the tablet over USB (leave it running)
condor alpine install|status|remove    Alpine Linux root on the tablet
condor alpine run <command>            run a command inside the tablet's Alpine
condor takeover status                 hook, autostart, condor-init, logs
condor takeover auto on|off            boot into condor every time / back to Android
condor takeover arm|disarm             one-shot takeover for the next boot / cancel
condor takeover push <bin> | restart   install a new condor-init / restart it in place
condor takeover install-hook           update the startup hook in /system (one file)
condor bootimg info|unpack|pack        Intel OSIP boot images (files on the PC)
condor recon | backup                  read-only inspection / copy the tablet's files
condor shell | reboot | screenshot     the usual adb helpers
```

On the tablet (Alpine): `wifi scan | connect SSID [password] | status | off | forget | debug`,
and `apk` for everything else.
