// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"errors"

	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/model"
)

// ErrRecordingNotFound reports that no recording row exists for the
// requested identifier.
var ErrRecordingNotFound = errors.New("recording not found")

var ErrInvalidRecordingFilter = errors.New("invalid recording filter or cursor")

// RecordingMetadataStore reads recording metadata; callers choose retrievable states.
type RecordingMetadataStore interface {
	// GetByID returns a recording or ErrRecordingNotFound, without filtering state.
	GetByID(ctx context.Context, recordingID string) (model.Recording, error)
	// Exists reports whether the recording is present.
	Exists(ctx context.Context, recordingID string) (bool, error)
}

// RecordingLifecycleStore is the optional write/list capability used by the
// stream recorder. Retrieval continues to depend only on RecordingMetadataStore.
type RecordingLifecycleStore interface {
	RecordingMetadataStore
	CreateMetadata(ctx context.Context, recording model.Recording) (model.Recording, error)
	GetMetadataByRecordingID(ctx context.Context, recordingID string) (model.Recording, error)
	UpdateMetadata(ctx context.Context, recordingID string, recording model.Recording) (model.Recording, error)
	ListMetadata(ctx context.Context, filter model.RecordingFilter) ([]model.Recording, string, error)
	DeleteMetadata(ctx context.Context, recordingID string) error
}
