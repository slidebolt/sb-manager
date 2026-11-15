package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/slidebolt/sb-manager/app"
)

func main() {
	defaultReadyTimeout := 30 * time.Second
	if raw := os.Getenv("SB_MANAGER_READY_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			fmt.Fprintln(os.Stderr, "invalid SB_MANAGER_READY_TIMEOUT:", raw)
			os.Exit(2)
		}
		defaultReadyTimeout = parsed
	}
	binDir := flag.String("bin-dir", ".bin", "directory to watch for binaries")
	overrideDir := flag.String("bin-override-dir", "", "optional directory to shadow canonical binaries")
	statusAddr := flag.String("status-addr", "127.0.0.1:29010", "read-only manager status listen address; empty disables it")
	readyTimeout := flag.Duration("ready-timeout", defaultReadyTimeout, "maximum time one component may take to become ready")
	configPath := flag.String("config", "", "optional component environment configuration JSON")
	flag.Parse()

	runtimeConfig, err := app.LoadRuntimeConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load manager config:", err)
		os.Exit(1)
	}
	app.Run(app.Config{BinDir: *binDir, OverrideDir: *overrideDir, StatusAddr: *statusAddr, ReadyTimeout: *readyTimeout, Components: runtimeConfig.Components})
}
