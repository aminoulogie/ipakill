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
const (
	bootfailPath = condorHome + "/bootfail"
	stableAfter  = 25 * time.Second
)

func markBootGood() {
	time.Sleep(stableAfter)
	if err := os.Remove(bootfailPath); err == nil {
		log.Printf("boot marked good (cleared %s)", bootfailPath)
	}
}
