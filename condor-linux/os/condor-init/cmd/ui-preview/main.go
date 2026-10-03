// ui-preview renders condor's interface to PNG files so the layout can be checked on a PC
// before it goes to the tablet:
//
//	go run ./cmd/ui-preview            writes home.png and app.png in the current folder
package main

import (
	"image"
	"image/png"
	"log"
	"os"
	"time"

	"condor-init/ui"
)

func main() {
	l, err := ui.NewLauncher(1200, 1920, ui.DefaultApps)
	if err != nil {
		log.Fatal(err)
	}
	st := ui.Status{Time: time.Now(), Battery: 73, Charging: true}
	save("home.png", l.Home(st))
	save("app.png", l.AppScreen(ui.DefaultApps[0], st))
}

func save(name string, img image.Image) {
	f, err := os.Create(name)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", name)
}
