package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/docker"
	"github.com/triangles/polyon-core/internal/monitor"
	"github.com/triangles/polyon-core/internal/server"
)

func main() {
	// Configure zerolog
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"}).
		With().Timestamp().Caller().Logger()

	log.Info().Msg("PolyON Core (Go) starting...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load config")
	}

	// Create server
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create server")
	}

	// Start monitors after a brief delay (let services boot)
	go func() {
		time.Sleep(5 * time.Second)

		st := srv.Store()

		// Start health checker (Docker-based container monitoring)
		dc, dcErr := docker.New()
		if dcErr != nil {
			log.Warn().Err(dcErr).Msg("Docker client unavailable for health checker")
		}
		monitor.Start(dc, st)

		// Start sentinel agent (LLM-based analysis)
		monitor.StartSentinelAgent(cfg.SharedDir, st)
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil {
			log.Info().Err(err).Msg("Server stopped")
		}
	}()

	<-quit
	log.Info().Msg("Shutting down...")

	monitor.Stop()
	monitor.StopSentinelAgent()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
