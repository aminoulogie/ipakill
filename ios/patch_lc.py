#!/usr/bin/env python3
"""Turns a LiveContainer checkout into ipakill.

ipakill runs other apps inside itself the way LiveContainer does, by building
on LiveContainer's engine (AGPL-3.0, https://github.com/LiveContainer/LiveContainer).
This script, run by CI on a pinned LiveContainer commit:
  - copies ipakill's Swift files into LiveContainer's UI framework,
  - puts ipakill's tabs (Sources, Library, Activity) in its tab bar next to
    LiveContainer's Apps and Settings tabs,
  - renames the app to ipakill (bundle id, name, icon).

Every edit asserts that the text it replaces is still there, so moving to a
newer LiveContainer commit fails loudly instead of building something odd.

usage: patch_lc.py <livecontainer checkout> <version>
"""
import pathlib
import plistlib
import shutil
import sys

lc = pathlib.Path(sys.argv[1])
version = sys.argv[2]
here = pathlib.Path(__file__).resolve().parent


def edit(rel, old, new):
    p = lc / rel
    s = p.read_text()
    if old not in s:
        sys.exit(f"patch_lc: {rel} changed upstream, cannot find:\n{old}")
    p.write_text(s.replace(old, new, 1))


# ipakill's code, compiled into the LiveContainerSwiftUI framework (its folder
# is synced into the target, so new files are picked up without project edits).
dst = lc / "LiveContainerSwiftUI" / "ipakill"
dst.mkdir(exist_ok=True)
for name in ["Store.swift", "Sync.swift", "ContentView.swift"]:
    shutil.copy(here / "ipakill" / name, dst / name)

edit("LiveContainerSwiftUI/Utilities/Shared.swift",
     "    case settings\n}",
     "    case settings\n    case ipakillLibrary\n    case ipakillActivity\n}")

tab = "LiveContainerSwiftUI/Views/LCTabView.swift"
edit(tab,
     "    @StateObject var downloadHelper = DownloadHelper()\n",
     "    @StateObject var downloadHelper = DownloadHelper()\n"
     "    @StateObject var ipakillSync = Sync()\n")
# ipakill's Sources replace LiveContainer's.
edit(tab,
     """            if DataManager.shared.model.multiLCStatus != 2 {
                LCSourcesView()
                    .tabItem {
                        Label("lc.tabView.sources".loc, systemImage: "books.vertical")
                    }
                    .tag(LCTabIdentifier.sources)
            }
""",
     """            IpakillSourcesView()
                .tabItem {
                    Label("Sources", systemImage: "square.stack.3d.up")
                }
                .tag(LCTabIdentifier.sources)
""")
# Library and Activity take the place of Tweaks.
edit(tab,
     """            if DataManager.shared.model.multiLCStatus != 2 {
                LCTweaksView()
                    .tabItem{
                        Label("lc.tabView.tweaks".loc, systemImage: "wrench.and.screwdriver")
                    }
                    .tag(LCTabIdentifier.tweaks)
            }
""",
     """            IpakillLibraryView()
                .tabItem {
                    Label("Library", systemImage: "square.grid.2x2.fill")
                }
                .badge(ipakillSync.updateCount)
                .tag(LCTabIdentifier.ipakillLibrary)
            IpakillActivityView()
                .tabItem {
                    Label("Activity", systemImage: "arrow.down.circle.fill")
                }
                .tag(LCTabIdentifier.ipakillActivity)
""")
edit(tab,
     "        .environmentObject(downloadHelper)\n",
     "        .environmentObject(downloadHelper)\n"
     "        .modifier(IpakillLifecycle())\n"
     "        .environmentObject(ipakillSync)\n")

# Branding.
edit("xcconfigs/Global.xcconfig",
     "LIVECONTAINER_BUNDLE_IDENTIFIER = com.kdt.livecontainer$(DEVELOPMENT_TEAM_SUFFIX)",
     "LIVECONTAINER_BUNDLE_IDENTIFIER = io.github.aminoulogie.ipakill")

info_path = lc / "LiveContainer" / "Info.plist"
with open(info_path, "rb") as f:
    info = plistlib.load(f)
info["CFBundleDisplayName"] = "ipakill"
info["CFBundleName"] = "ipakill"
info["CFBundleShortVersionString"] = version
info["CFBundleVersion"] = version
info["NSLocalNetworkUsageDescription"] = (
    "ipakill talks to your PC over Wi-Fi to sign apps. Apps running inside ipakill may also use the local network.")
with open(info_path, "wb") as f:
    plistlib.dump(info, f)

icons = lc / "Resources" / "Assets.xcassets" / "AppIcon.appiconset"
for png in icons.glob("*.png"):
    shutil.copy(here / "ipakill" / "Assets.xcassets" / "AppIcon.appiconset" / "icon-1024.png", png)

print("patch_lc: ok")
