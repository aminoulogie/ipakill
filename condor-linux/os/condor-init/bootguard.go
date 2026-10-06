package main

import (
	"log"
	"os"
	"time"
)

// bootfailPath is the crash-loop counter the /system hook bumps before starting us (see
// cli/takeover.go). markBootGood clears it once we've run long enough to be considered stable,
// so a healthy boot resets the strike count. If condor-init instead dies before this fires,
// the counter keeps climbing and the hook falls back to Android, so a bad build can never
// trap the tablet in a boot loop.
//
// stableAfter is short on purpose. A genuinely broken build crashes in the first second or
// two (a panic in init, the framebuffer or fonts failing to open); ten seconds is plenty to
// catch that. Waiting longer only hurts the honest case we actually hit: on a nearly-dead
// battery condor-init starts fine but the tablet loses power before the timer fires, which
// looks exactly like a crash to the counter. A short window plus the hook's higher strike
// limit means a few power-starved boots in a row no longer strand the tablet in Android.
const (
	bootfailPath = condorHome + "/bootfail"
	stableAfter  = 10 * time.Second
)

func markBootGood() {
	time.Sleep(stableAfter)
	if err := os.Remove(bootfailPath); err == nil {
		log.Printf("boot marked good (cleared %s)", bootfailPath)
	}
}
