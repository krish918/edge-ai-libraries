// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

// Command api-server runs the Stream Manager HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/api"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/config"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/logging"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/mediaaccess"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/record"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/replay"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/storage"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/stream"
)

// startupTimeout bounds the backend reachability checks so a wedged
// dependency surfaces as a failed start rather than a hung process.
const startupTimeout = 30 * time.Second

const (
	serverReadHeaderTimeout = 10 * time.Second
	serverReadTimeout       = 30 * time.Second
	serverWriteTimeout      = 10 * time.Minute
	serverIdleTimeout       = 2 * time.Minute
	serverShutdownTimeout   = 30 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("stream manager stopped", "error", err)
		os.Exit(1)
	}
}

// run initializes dependencies and returns after shutdown so deferred cleanup completes.
func run() (result error) {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger, err := logging.New(cfg.LogFormat, cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("configure logging: %w", err)
	}
	slog.SetDefault(logger)

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	metadataStore, err := storage.OpenSQLiteMetadataStore(ctx, cfg.SQLitePath)
	if err != nil {
		return fmt.Errorf("open metadata store: %w", err)
	}
	defer func() { result = errors.Join(result, metadataStore.Close()) }()

	mediaStore, mediaSigner, err := buildMediaStore(cfg)
	if err != nil {
		return fmt.Errorf("configure media store: %w", err)
	}
	if closer, ok := mediaStore.(interface{ Close() error }); ok {
		defer func() { result = errors.Join(result, closer.Close()) }()
	}

	// The media backend is provisioned by the deployment, not by this
	// service, so startup only confirms it is reachable.
	if err := mediaStore.Health(ctx); err != nil {
		return fmt.Errorf("media store not reachable: %w", err)
	}
	if err := metadataStore.Health(ctx); err != nil {
		return fmt.Errorf("metadata store not reachable: %w", err)
	}

	buffers, err := stream.NewService(cfg)
	if err != nil {
		return fmt.Errorf("configure stream buffers: %w", err)
	}
	defer func() { result = errors.Join(result, buffers.Close()) }()

	var recordings *record.Service
	if cfg.StorageBackend == config.StorageBackendFilesystem {
		liveStore, ok := mediaStore.(storage.LiveRecordingLifecycleMediaStore)
		if !ok {
			return errors.New("configure recorder: filesystem backend does not support live recording writes")
		}
		recordings, err = record.NewService(buffers, metadataStore, liveStore, cfg.MaxActiveRecords, cfg.AllowBestEffortTimestamps)
		if err != nil {
			return fmt.Errorf("configure recorder: %w", err)
		}
		defer func() { result = errors.Join(result, recordings.Close()) }()
		if err := recordings.RecoverInterrupted(ctx); err != nil {
			return fmt.Errorf("recover interrupted recordings: %w", err)
		}
	}

	service := replay.NewRetrievalService(metadataStore, mediaStore, replay.Options{
		DerivedTTL:    cfg.DerivedTTL,
		PresignExpiry: cfg.PresignExpiry,
		MaxStageBytes: cfg.FSMaxStageBytes,
	})
	router := api.NewRouter(service, mediaStore, cfg.Version, logger, mediaSigner, buffers, recordings)

	logger.Info("listening",
		"service", "stream-manager",
		"version", cfg.Version,
		"storage_backend", cfg.StorageBackend,
		"fs_root", cfg.FSRoot,
		"sqlite_path", cfg.SQLitePath,
		"port", cfg.Port,
	)

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
	serverCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveHTTP(serverCtx, server, listener, logger)
}

// serveHTTP serves requests until the listener fails or cancellation drains the server.
func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener, logger *slog.Logger) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			logger.Warn("graceful HTTP shutdown timed out; closing active connections", "error", shutdownErr)
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		serveErr := <-serveErr
		if !errors.Is(serveErr, http.ErrServerClosed) {
			shutdownErr = errors.Join(shutdownErr, serveErr)
		}
		return shutdownErr
	}
}

// buildMediaStore creates the configured backend and its media URL signer.
// Filesystem storage requires the signer.
func buildMediaStore(cfg config.Config) (storage.MediaStore, *mediaaccess.Signer, error) {
	var signer *mediaaccess.Signer
	if strings.TrimSpace(cfg.MediaTokenSecret) != "" {
		var err error
		signer, err = mediaaccess.NewSigner(cfg.MediaTokenSecret)
		if err != nil {
			return nil, nil, err
		}
	}

	switch cfg.StorageBackend {
	case config.StorageBackendFilesystem:
		store, err := storage.NewFileMediaStore(cfg.FSRoot, cfg.PublicBaseURL, signer)
		if err != nil {
			return nil, nil, err
		}
		return store, signer, nil
	default:
		return nil, nil, fmt.Errorf("unsupported storage backend %q", cfg.StorageBackend)
	}
}
