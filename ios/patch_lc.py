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

# One-jump Home Screen launch (no extension, no app ID): a Shortcut saves
# ipakill-launch.txt (an app's name) into ipakill's Documents and opens
# ipakill; on a cold start we read it before anything loads and boot straight
# into that app. A note older than 30 s is ignored, any note is deleted.
edit("LiveContainer/LCBootstrap.m",
     """    NSString *selectedContainer = [lcUserDefaults stringForKey:@"selectedContainer"];
    NSString *launchUrl = nil;
""",
     """    NSString *selectedContainer = [lcUserDefaults stringForKey:@"selectedContainer"];
    NSString *launchUrl = nil;
    do {
        NSFileManager *fm = NSFileManager.defaultManager;
        NSString *docs = [NSString stringWithFormat:@"%s/Documents", getenv("LC_HOME_PATH")];
        NSString *note = [docs stringByAppendingPathComponent:@"ipakill-launch.txt"];
        NSDate *written = [fm attributesOfItemAtPath:note error:nil].fileModificationDate;
        if(!written) break;
        NSString *wanted = [[NSString stringWithContentsOfFile:note encoding:NSUTF8StringEncoding error:nil]
                            stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
        [fm removeItemAtPath:note error:nil];
        if(selectedApp || -written.timeIntervalSinceNow > 30 || wanted.length == 0) break;
        // Accept the bundle folder name or the app's name, any case.
        NSString *apps = [docs stringByAppendingPathComponent:@"Applications"];
        for(NSString *folder in [fm contentsOfDirectoryAtPath:apps error:nil]) {
            if(![folder hasSuffix:@".app"]) continue;
            NSDictionary *info = [NSDictionary dictionaryWithContentsOfFile:[NSString stringWithFormat:@"%@/%@/Info.plist", apps, folder]];
            for(NSString *name in @[folder, [folder stringByDeletingPathExtension], info[@"CFBundleDisplayName"] ?: @"", info[@"CFBundleName"] ?: @""]) {
                if([name caseInsensitiveCompare:wanted] == NSOrderedSame) {
                    selectedApp = folder;
                    break;
                }
            }
            if(selectedApp) break;
        }
    } while(0);
""")

# Home Screen icons made by LiveContainer's "Add to Home Screen" profile
# open ipakill with a launch link. On a cold start, restart straight into the
# app before any screen is drawn (ipakillFastLaunch in ContentView.swift).
edit("LiveContainerSwiftUI/App/AppDelegate.swift",
     """        self.window = (scene as? UIWindowScene)?.keyWindow
    }""",
     """        self.window = (scene as? UIWindowScene)?.keyWindow
        ipakillFastLaunch(connectionOptions)
    }""")

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
