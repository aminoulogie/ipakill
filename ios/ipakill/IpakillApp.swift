import SwiftUI

@main
struct IpakillApp: App {
    @StateObject private var sync = Sync()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(sync)
                .preferredColorScheme(.dark)
        }
    }
}
