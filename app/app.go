package app

import (
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	managercore "github.com/slidebolt/sb-manager/internal/manager"
)

type Config struct {
	BinDir      string
	OverrideDir string
}

func Run(cfg Config) {
	// Structured JSON logging to stderr at DEBUG level.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})).With("binary", "manager")
	slog.SetDefault(logger)
	log.SetFlags(0)

	m := managercore.New(cfg.BinDir, cfg.OverrideDir)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigs
		slog.Info("received signal, shutting down")
		m.Shutdown()
		os.Exit(0)
	}()

	slog.Info("sb-manager starting", "bin_dir", cfg.BinDir)
	if err := m.Run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
