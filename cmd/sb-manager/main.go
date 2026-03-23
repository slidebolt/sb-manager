package main

import (
	"flag"

	"github.com/slidebolt/sb-manager/app"
)

func main() {
	binDir := flag.String("bin-dir", ".bin", "directory to watch for binaries")
	flag.Parse()

	app.Run(app.Config{BinDir: *binDir})
}
