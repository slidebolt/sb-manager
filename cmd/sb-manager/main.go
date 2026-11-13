package main

import (
	"flag"

	"github.com/slidebolt/sb-manager/app"
)

func main() {
	binDir := flag.String("bin-dir", ".bin", "directory to watch for binaries")
	overrideDir := flag.String("bin-override-dir", "", "optional directory to shadow canonical binaries")
	statusAddr := flag.String("status-addr", "127.0.0.1:29010", "read-only manager status listen address; empty disables it")
	flag.Parse()

	app.Run(app.Config{BinDir: *binDir, OverrideDir: *overrideDir, StatusAddr: *statusAddr})
}
