// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

// Package config loads service configuration from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Default derived-object and presigned-URL lifetimes. Derived assets are
// reproducible from the source recording, so they are kept only long enough
// for a client to follow the URL it was handed.
const (
	DefaultDerivedTTL    = 15 * time.Minute
	DefaultPresignExpiry = 5 * time.Minute
)

// DefaultVersion is reported by GET /v1/version when STREAM_MANAGER_VERSION
// is not set. Deployments override it with the released tag.
const DefaultVersion = "0.1.0"

// Storage backend selector for STREAM_MANAGER_STORAGE_BACKEND.
const (
	StorageBackendFilesystem = "filesystem"
)

// DefaultFSMaxStageBytes bounds how large a source recording may be before
// full-file staging (copying it whole into a temporary file ahead of
// extraction) is refused. It exists to keep a long live recording from
// exhausting local disk.
const DefaultFSMaxStageBytes int64 = 2 << 30 // 2 GiB

const (
	DefaultHTTPAddr         = ":8080"
	DefaultStorageDir       = "~/.local/share/stream-manager"
	DefaultBufferDir        = "/dev/shm/stream-manager"
	DefaultBufferLength     = 30 * time.Second
	MinBufferLength         = 5 * time.Second
	MaxBufferLength         = 300 * time.Second
	DefaultStorageSize      = 10240 // Megabytes
	MinStorageSize          = 5120  // Megabytes
	ConcurrentRecorders     = 10
	DefaultMaxActiveRecords = ConcurrentRecorders
)

// Config holds the settings needed to bootstrap the metadata and media
// backends.
type Config struct {
	// Version is the service version reported by GET /v1/version.
	Version string

	// LogLevel is debug, info, warn, or error. LogFormat is json or text.
	LogLevel  string
	LogFormat string

	// Port is the HTTP listen port for the API server.
	Port string

	// SQLitePath is the filesystem path to the SQLite metadata database.
	SQLitePath string

	// DerivedTTL is the retention hint applied to derived frame/clip
	// objects when they are published.
	DerivedTTL time.Duration
	// PresignExpiry is how long an issued media URL stays valid.
	PresignExpiry time.Duration

	// StorageBackend selects the filesystem MediaStore.
	StorageBackend string
	// FSRoot is the local directory backing the filesystem MediaStore.
	// Defaults to the expanded DefaultStorageDir when unset.
	FSRoot string
	// PublicBaseURL is this service's externally reachable base URL, used
	// to build GET /v1/media/{token} capability URLs. Required when
	// StorageBackend is "filesystem".
	PublicBaseURL string
	// MediaTokenSecret signs and verifies GET /v1/media/{token} capability
	// tokens. Required when StorageBackend is "filesystem".
	MediaTokenSecret string
	// FSMaxStageBytes bounds full-file staging of a live source recording
	// ahead of extraction.
	FSMaxStageBytes int64

	// BufferLength is the rolling RTSP history window. BufferDir must be
	// private tmpfs; HTTPAddr is derived from Port for the unified server.
	BufferLength time.Duration
	BufferDir    string
	HTTPAddr     string

	// MaxActiveRecords bounds concurrent recording workers.
	MaxActiveRecords int
	// RecordingStorageSize retains the legacy setting; no storage quota is enforced.
	RecordingStorageSize int

	// AllowBestEffortTimestamps enables a development-only wall-clock anchor
	// when RTCP/NTP mapping is unavailable. Production should leave it false.
	AllowBestEffortTimestamps bool
}

// Load reads configuration from environment variables. Filesystem storage is
// the only supported backend.
func Load() (Config, error) {
	derivedTTL, err := getEnvDuration("STREAM_MANAGER_DERIVED_TTL", DefaultDerivedTTL)
	if err != nil {
		return Config{}, err
	}
	presignExpiry, err := getEnvDuration("STREAM_MANAGER_PRESIGN_EXPIRY", DefaultPresignExpiry)
	if err != nil {
		return Config{}, err
	}
	fsMaxStageBytes, err := getEnvInt64("STREAM_MANAGER_FS_MAX_STAGE_BYTES", DefaultFSMaxStageBytes)
	if err != nil {
		return Config{}, err
	}
	bufferLength, err := getEnvDuration("STREAM_MANAGER_BUFFER_LENGTH", DefaultBufferLength)
	if err != nil {
		return Config{}, err
	}
	if bufferLength < MinBufferLength || bufferLength > MaxBufferLength {
		return Config{}, fmt.Errorf("STREAM_MANAGER_BUFFER_LENGTH must be between %s and %s", MinBufferLength, MaxBufferLength)
	}
	maxActiveRecords, err := getEnvPositiveInt("STREAM_MANAGER_MAX_ACTIVE_RECORDS", DefaultMaxActiveRecords)
	if err != nil {
		return Config{}, err
	}
	recordingStorageSize := DefaultStorageSize
	if raw := strings.TrimSpace(os.Getenv("SM_RECORDING_STORAGE")); raw != "" {
		recordingStorageSize, err = strconv.Atoi(raw)
		if err != nil || recordingStorageSize < MinStorageSize {
			return Config{}, fmt.Errorf("SM_RECORDING_STORAGE must be an integer greater than or equal to %d", MinStorageSize)
		}
	}
	storageDir := os.Getenv("STREAM_MANAGER_FS_ROOT")
	if storageDir == "" {
		storageDir, err = expandHome(DefaultStorageDir)
		if err != nil {
			return Config{}, err
		}
	}
	httpAddr := DefaultHTTPAddr
	if port := os.Getenv("STREAM_MANAGER_PORT"); port != "" {
		httpAddr = ":" + port
	}
	if addr := strings.TrimSpace(os.Getenv("SM_HTTP_ADDR")); addr != "" {
		httpAddr = addr
	}
	allowBestEffortTimestamps, err := getEnvBool("STREAM_MANAGER_DEV_BEST_EFFORT_TIMESTAMPS", false)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Version:    getEnvDefault("STREAM_MANAGER_VERSION", DefaultVersion),
		LogLevel:   getEnvDefault("STREAM_MANAGER_LOG_LEVEL", "info"),
		LogFormat:  getEnvDefault("STREAM_MANAGER_LOG_FORMAT", "json"),
		Port:       getEnvDefault("STREAM_MANAGER_PORT", "8080"),
		SQLitePath: getEnvDefault("STREAM_MANAGER_SQLITE_PATH", "/var/lib/stream-manager/stream-manager.db"),

		DerivedTTL:    derivedTTL,
		PresignExpiry: presignExpiry,

		StorageBackend:            strings.ToLower(getEnvDefault("STREAM_MANAGER_STORAGE_BACKEND", StorageBackendFilesystem)),
		FSRoot:                    storageDir,
		PublicBaseURL:             strings.TrimRight(os.Getenv("STREAM_MANAGER_PUBLIC_BASE_URL"), "/"),
		MediaTokenSecret:          os.Getenv("STREAM_MANAGER_MEDIA_TOKEN_SECRET"),
		FSMaxStageBytes:           fsMaxStageBytes,
		BufferLength:              bufferLength,
		BufferDir:                 getEnvDefault("STREAM_MANAGER_BUFFER_DIR", DefaultBufferDir),
		HTTPAddr:                  httpAddr,
		MaxActiveRecords:          maxActiveRecords,
		RecordingStorageSize:      recordingStorageSize,
		AllowBestEffortTimestamps: allowBestEffortTimestamps,
	}
	if !filepath.IsAbs(cfg.SQLitePath) {
		return Config{}, fmt.Errorf("STREAM_MANAGER_SQLITE_PATH must be an absolute path")
	}

	switch cfg.StorageBackend {
	case StorageBackendFilesystem:
		if strings.TrimSpace(cfg.FSRoot) == "" {
			return Config{}, fmt.Errorf("STREAM_MANAGER_FS_ROOT is required when STREAM_MANAGER_STORAGE_BACKEND=filesystem")
		}
		if !filepath.IsAbs(cfg.FSRoot) {
			return Config{}, fmt.Errorf("STREAM_MANAGER_FS_ROOT must be an absolute path")
		}
		if strings.TrimSpace(cfg.PublicBaseURL) == "" {
			return Config{}, fmt.Errorf("STREAM_MANAGER_PUBLIC_BASE_URL is required when STREAM_MANAGER_STORAGE_BACKEND=filesystem")
		}
		if strings.TrimSpace(cfg.MediaTokenSecret) == "" {
			return Config{}, fmt.Errorf("STREAM_MANAGER_MEDIA_TOKEN_SECRET is required when STREAM_MANAGER_STORAGE_BACKEND=filesystem")
		}
	default:
		return Config{}, fmt.Errorf("STREAM_MANAGER_STORAGE_BACKEND must be %q, got %q", StorageBackendFilesystem, cfg.StorageBackend)
	}

	return cfg, nil
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func expandHome(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for %s: %w", path, err)
	}
	if !filepath.IsAbs(homeDir) {
		return "", fmt.Errorf("home directory %q must be absolute", homeDir)
	}
	return filepath.Join(homeDir, rest), nil
}

func getEnvBool(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}

func getEnvInt64(key string, fallback int64) (int64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return parsed, nil
}

func getEnvPositiveInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func getEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration (e.g. \"5m\"): %w", key, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return parsed, nil
}
