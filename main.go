// Package main is the rivus entry point — it loads config, builds the proxy
// server, and handles OS signals for graceful shutdown.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adevsh/rivus/config"
	"github.com/adevsh/rivus/proxy"
)

func main() {
	logger := setupLogger()

	cfgPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("failed to load config", "config", *cfgPath, "error", err)
		os.Exit(1)
	}

	server, err := proxy.New(cfg)
	if err != nil {
		logger.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	logger.Info("server starting", "listen", cfg.Listen, "tls_enabled", cfg.TLS.Enabled)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server terminated with error", "error", err)
			os.Exit(1)
		}
		logger.Info("server exited")
	case <-ctx.Done():
		logger.Info("shutdown signal received", "signal", ctx.Err())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown failed", "error", err)
			os.Exit(1)
		}

		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server exit after shutdown", "error", err)
			os.Exit(1)
		}
		logger.Info("shutdown complete")
	}
}

func setupLogger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	var handler slog.Handler
	if os.Getenv("RIVUS_ENV") == "production" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
