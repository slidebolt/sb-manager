package main

import (
	"flag"

	"github.com/slidebolt/sb-manager/app"
)

func main() {
	binDir := flag.String("bin-dir", ".bin", "directory to watch for binaries")
	overrideDir := flag.String("bin-override-dir", "", "optional directory to shadow canonical binaries")
	flag.Parse()

	app.Run(app.Config{BinDir: *binDir, OverrideDir: *overrideDir})
}
