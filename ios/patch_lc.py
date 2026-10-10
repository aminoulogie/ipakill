#!/usr/bin/env python3
"""Build today's ipakill UI on top of yesterday's pinned LiveContainer engine.

The visible root stays ipakill's modern ContentView (Store/Library/Activity/
Terminal/Settings). LiveContainer remains embedded only as the app-running
engine used by the "LiveContainer" install choice.

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

# Copy today's ipakill UI/model code into LiveContainerSwiftUI so it compiles
# in the same target as SharedModel, LCAppListView, DownloadHelper, etc.
dst = lc / "LiveContainerSwiftUI" / "ipakill"
dst.mkdir(exist_ok=True)
for name in ["Store.swift", "Sync.swift", "ContentView.swift"]:
    shutil.copy(here / "ipakill" / name, dst / name)

# Keep LiveContainer's initialization/AppDelegate, but make today's ipakill
# ContentView the visible root. The embedded LiveContainer app list is opened
# only by today's LiveContainer install choice.
app = "LiveContainerSwiftUI/App/LiveContainerSwiftUIApp.swift"
edit(app,
     "    @UIApplicationDelegateAdaptor(AppDelegate.self) var appDelegate\n",
     "    @UIApplicationDelegateAdaptor(AppDelegate.self) var appDelegate\n"
     "    @StateObject private var ipakillSync = Sync()\n")

edit(app,
     """            LCTabView()
                .handlesExternalEvents(preferring: ["*"], allowing: ["*"])
                .environmentObject(DataManager.shared.model)
                .environmentObject(LCAppSortManager.shared)
""",
     """            ContentView()
                .handlesExternalEvents(preferring: ["*"], allowing: ["*"])
                .environmentObject(ipakillSync)
                .environmentObject(DataManager.shared.model)
                .environmentObject(LCAppSortManager.shared)
""")

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
    "ipakill talks to your PC over Wi-Fi for full sideloading and optional "
    "certificate import. LiveContainer apps run locally once configured."
)
with open(info_path, "wb") as f:
    plistlib.dump(info, f)

icons = lc / "Resources" / "Assets.xcassets" / "AppIcon.appiconset"
source_icon = here / "ipakill" / "Assets.xcassets" / "AppIcon.appiconset" / "icon-1024.png"
for png in icons.glob("*.png"):
    shutil.copy(source_icon, png)

print("patch_lc: modern ipakill UI + embedded LiveContainer engine")
