// SPDX-FileCopyrightText: (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

// Package cleanup removes expired and orphaned recording media.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/model"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/storage"
)

const (
	cleanupInterval = 24 * time.Hour
	batchSize       = 100
)

type Service struct {
	metadata  storage.MetadataHandler
	media     storage.MediaHandler
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

// NewService starts cleanup immediately and repeats it every 24 hours.
func NewService(metadata storage.MetadataHandler, media storage.MediaHandler) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		metadata: metadata, media: media,
		cancel: cancel, done: make(chan struct{}),
	}
	go s.run(ctx)
	return s
}

func (s *Service) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		if err := s.sweep(ctx, time.Now()); err != nil && ctx.Err() == nil {
			slog.Error("media cleanup failed; will retry on the next run", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) sweep(ctx context.Context, now time.Time) error {
	result := s.expireRecordings(ctx, now)
	directories, err := s.media.ListMedia(ctx)
	if err != nil {
		result = errors.Join(result, fmt.Errorf("list orphan candidates: %w", err))
	} else {
		for id, uri := range directories {
			if err := ctx.Err(); err != nil {
				return errors.Join(result, err)
			}
			result = errors.Join(result, s.cleanupRecording(ctx, id, uri, now))
		}
	}
	return result
}

func (s *Service) expireRecordings(ctx context.Context, now time.Time) error {
	var result error
	cursor := ""
	for {
		records, next, err := s.metadata.ListMetadata(ctx,
			model.RecordingFilter{ExpiryTS: now, Limit: batchSize, Cursor: cursor})
		if err != nil {
			return errors.Join(result, fmt.Errorf("list expired recordings: %w", err))
		}
		for _, rec := range records {
			if err := ctx.Err(); err != nil {
				return errors.Join(result, err)
			}
			result = errors.Join(result, s.cleanupRecording(ctx, rec.RecordingID, rec.RecordingPath, now))
		}
		if next == "" {
			return result
		}
		cursor = next
	}
}

func (s *Service) cleanupRecording(ctx context.Context, id, uri string, now time.Time) error {
	rec, err := s.metadata.GetMetadataByRecordingID(ctx, id)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("read recording %s for cleanup: %w", id, err)
	}
	if err == nil {
		if rec.State == "recording" || rec.State == "finalizing" ||
			rec.ExpiryTS == nil || now.Before(*rec.ExpiryTS) {
			return nil
		}
		uri = rec.RecordingPath
	}
	if err := s.media.DeleteSourceMedia(ctx, uri); errors.Is(err, storage.ErrMediaBusy) {
		slog.Debug("recording cleanup deferred while media is in use", "recording_id", id)
		return nil
	} else if err != nil {
		return fmt.Errorf("clean recording %s: %w", id, err)
	}
	if err := s.metadata.DeleteMetadata(ctx, id); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("delete recording metadata %s: %w", id, err)
	}
	return nil
}

func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		<-s.done
	})
	return nil
}
