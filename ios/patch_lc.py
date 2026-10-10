#!/usr/bin/env python3
"""Build today's ipakill UI on top of yesterday's pinned LiveContainer engine.

The visible root remains ipakill's current ContentView (Store, Library,
Activity, Terminal, Settings). LiveContainer stays embedded and is opened only
when the user chooses the LiveContainer install method.
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


# Compile ipakill's current Swift files inside LiveContainerSwiftUI so they can
# call SharedModel/LCUtils directly without an external URL-scheme handoff.
dst = lc / "LiveContainerSwiftUI" / "ipakill"
dst.mkdir(exist_ok=True)
for name in ["Store.swift", "Sync.swift", "ContentView.swift", "IPALabView.swift"]:
    shutil.copy(here / "ipakill" / name, dst / name)

# Keep LiveContainer initialized exactly as upstream does, but make today's
# ipakill ContentView the visible root. LCTabView is still compiled and is
# presented internally when the user selects the LiveContainer install path.
app = "LiveContainerSwiftUI/App/LiveContainerSwiftUIApp.swift"
edit(
    app,
    "    @UIApplicationDelegateAdaptor(AppDelegate.self) var appDelegate\n",
    "    @UIApplicationDelegateAdaptor(AppDelegate.self) var appDelegate\n"
    "    @StateObject private var ipakillSync = Sync()\n",
)
edit(
    app,
    """            LCTabView()
                .handlesExternalEvents(preferring: ["*"], allowing: ["*"])
                .environmentObject(DataManager.shared.model)
                .environmentObject(LCAppSortManager.shared)
""",
    """            ContentView()
                .environmentObject(ipakillSync)
                .environmentObject(DataManager.shared.model)
                .environmentObject(LCAppSortManager.shared)
                .preferredColorScheme(.dark)
                .handlesExternalEvents(preferring: ["*"], allowing: ["*"])
""",
)

# Today's UI uses NavigationStack/LabeledContent, so the embedded framework
# must target iOS 16 instead of the older iOS 15 minimum.
project = lc / "LiveContainer.xcodeproj" / "project.pbxproj"
project_text = project.read_text()
project_text = project_text.replace("IPHONEOS_DEPLOYMENT_TARGET = 15.0;", "IPHONEOS_DEPLOYMENT_TARGET = 16.0;")
project.write_text(project_text)

# Branding stays ipakill.
edit(
    "xcconfigs/Global.xcconfig",
    "LIVECONTAINER_BUNDLE_IDENTIFIER = com.kdt.livecontainer$(DEVELOPMENT_TEAM_SUFFIX)",
    "LIVECONTAINER_BUNDLE_IDENTIFIER = io.github.aminoulogie.ipakill",
)

info_path = lc / "LiveContainer" / "Info.plist"
with open(info_path, "rb") as f:
    info = plistlib.load(f)
info["CFBundleDisplayName"] = "ipakill"
info["CFBundleName"] = "ipakill"
info["CFBundleShortVersionString"] = version
info["CFBundleVersion"] = version
info["NSLocalNetworkUsageDescription"] = (
    "ipakill talks to your PC over Wi-Fi for physical sideloading and terminal access. "
    "LiveContainer installs run locally after its signing certificate has been imported."
)
with open(info_path, "wb") as f:
    plistlib.dump(info, f)

icons = lc / "Resources" / "Assets.xcassets" / "AppIcon.appiconset"
source_icon = here / "ipakill" / "Assets.xcassets" / "AppIcon.appiconset" / "icon-1024.png"
for png in icons.glob("*.png"):
    shutil.copy(source_icon, png)

print("patch_lc: merged current ipakill UI with embedded LiveContainer")
