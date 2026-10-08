// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

// Package storage defines media and recording-metadata storage interfaces.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrObjectNotFound = errors.New("object not found")

// MediaStore abstracts source and derived-media I/O. Implementations map
// logical keys to backend storage; retrieval opens sources and publishes
// derived output. Source Put methods also support fixture staging.
type MediaStore interface {
	// OpenRecording opens the source recording; callers must close the reader.
	OpenRecording(ctx context.Context, recordingPath string) (io.ReadCloser, error)
	// OpenSidecar opens the sidecar; callers parse and validate its contents.
	OpenSidecar(ctx context.Context, recordingID string) (io.ReadCloser, error)
	// OpenDerived opens a published frame or clip by key.
	OpenDerived(ctx context.Context, key string) (io.ReadCloser, error)

	// PutRecording stores source media and returns its logical key.
	PutRecording(ctx context.Context, recordingID string, media io.Reader, contentType string) (string, error)
	// PutSidecar stores a recording's sidecar and returns its logical key.
	PutSidecar(ctx context.Context, recordingID string, sidecar io.Reader) (string, error)

	// PutDerived stores derived media; ttl is a backend retention hint.
	PutDerived(ctx context.Context, key string, media io.Reader, contentType string, ttl time.Duration) error
	// DerivedExists reports whether derived media is already stored.
	DerivedExists(ctx context.Context, key string) (bool, error)
	// PresignDerived returns a short-lived URL for derived media.
	PresignDerived(ctx context.Context, key string, expiresIn time.Duration) (string, time.Time, error)

	// DeleteRecordingObjects removes source, sidecar, and derived media.
	DeleteRecordingObjects(ctx context.Context, recordingID string) error
	// Health checks backend availability for startup readiness.
	Health(ctx context.Context) error
}

// RecordingSidecarMediaStore selects a sidecar by source format when supported.
type RecordingSidecarMediaStore interface {
	OpenSidecarForRecording(ctx context.Context, recordingID, recordingPath string) (io.ReadCloser, error)
}

// LiveRecordingMediaStore writes live MPEG-TS recordings and JSONL sidecars.
type LiveRecordingMediaStore interface {
	PutLiveRecording(ctx context.Context, recordingID string, media io.Reader) (string, error)
	PutLiveSidecar(ctx context.Context, recordingID string, sidecar io.Reader) (string, error)
}

// LiveRecordingWriterStore creates, writes, and finalizes live media and sidecars.
type LiveRecordingWriterStore interface {
	PrepareLiveRecording(ctx context.Context, recordingID string) (filename, recordingPath string, err error)
	OpenLiveSidecarWriter(ctx context.Context, recordingID, recordingPath string) (io.WriteCloser, error)
	FinalizeLiveRecording(ctx context.Context, recordingPath string) (sizeBytes int64, err error)
}

// LiveRecordingLifecycleMediaStore provides the media operations needed by recording.
type LiveRecordingLifecycleMediaStore interface {
	LiveRecordingWriterStore
	DeleteRecordingObjects(ctx context.Context, recordingID string) error
}
