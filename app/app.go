package app

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	managercore "github.com/slidebolt/sb-manager/internal/manager"
)

type Config struct {
	BinDir      string
	OverrideDir string
	StatusAddr  string
}

func Run(cfg Config) {
	// Structured JSON logging to stderr at DEBUG level.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})).With("binary", "manager")
	slog.SetDefault(logger)
	log.SetFlags(0)

	m := managercore.New(cfg.BinDir, cfg.OverrideDir)
	var statusServer *http.Server
	if cfg.StatusAddr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /status/components", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(m.Status())
		})
		statusServer = &http.Server{Addr: cfg.StatusAddr, Handler: mux}
		go func() {
			if err := statusServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("manager status server failed", "error", err)
			}
		}()
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigs
		slog.Info("received signal, shutting down")
		m.Shutdown()
		if statusServer != nil {
			_ = statusServer.Close()
		}
		os.Exit(0)
	}()

	slog.Info("sb-manager starting", "bin_dir", cfg.BinDir)
	if err := m.Run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
