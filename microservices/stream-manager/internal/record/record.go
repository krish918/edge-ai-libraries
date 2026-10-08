// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

// Package record handles recording of attached streams.
package record

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/model"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/storage"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/stream"
)

var (
	ErrInvalidRequest = errors.New("invalid recording request")
	ErrConflict       = errors.New("recording cannot be changed in its current state")
	ErrCapacity       = errors.New("recording capacity reached")
	ErrCleanup        = errors.New("recording storage cleanup failed")
)

// StartOptions describes one recording of a single attached stream.
// TODO: accept several stream IDs and admit their recordings atomically.
type StartOptions struct {
	StreamID string
	StartTS  time.Time
	Duration *time.Duration
	PreEvent time.Duration
	Metadata map[string]any
}

// Service owns recording workers; its metadata and live-media stores outlive Close.
type Service struct {
	mu              sync.Mutex
	buffers         stream.Bufferer
	metadata        storage.RecordingLifecycleStore
	media           storage.LiveRecordingLifecycleMediaStore
	ffmpeg          string
	ffprobe         string
	max             int
	allowBestEffort bool
	jobs            map[string]*job
	wg              sync.WaitGroup
	closed          bool
	closeOnce       sync.Once
	closeErr        error
	now             func() time.Time
}

type job struct {
	record   model.Recording
	id       string
	lease    *stream.BufferLease
	filename string
	fixed    bool
	stopped  bool
	ctx      context.Context
	cancel   context.CancelFunc
	err      error
}

func NewService(buffers stream.Bufferer, metadata storage.RecordingLifecycleStore, media storage.LiveRecordingLifecycleMediaStore, concurrentRecordings int, allowBestEffort bool) (*Service, error) {
	if concurrentRecordings < 1 || concurrentRecordings > 1024 {
		return nil, ErrInvalidRequest
	}
	if buffers == nil || metadata == nil || media == nil {
		return nil, ErrInvalidRequest
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, err
	}
	return &Service{
		buffers: buffers, metadata: metadata, media: media, ffmpeg: ffmpeg, ffprobe: ffprobe, max: concurrentRecordings,
		allowBestEffort: allowBestEffort,
		jobs:            make(map[string]*job), now: time.Now,
	}, nil
}

// RecoverInterrupted marks recordings left active by a previous process as
// failed and removes their partial source and sidecar objects.
func (s *Service) RecoverInterrupted(ctx context.Context) error {
	for _, state := range []string{model.RecordingStateRecording, model.RecordingStateFinalizing} {
		cursor := ""
		for {
			recordings, next, err := s.metadata.ListMetadata(ctx, model.RecordingFilter{State: state, Limit: 100, Cursor: cursor})
			if err != nil {
				return err
			}
			for _, recording := range recordings {
				recording.State = model.RecordingStateFailed
				recording.SizeBytes = 0
				recording.ErrorDetails = "recording interrupted by service restart"
				if _, err := s.metadata.UpdateMetadata(ctx, recording.RecordingID, recording); err != nil {
					return err
				}
				if err := s.media.DeleteRecordingObjects(ctx, recording.RecordingID); err != nil {
					return fmt.Errorf("clean interrupted recording %s: %w", recording.RecordingID, err)
				}
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}
	return nil
}

func Seconds(value float64) (time.Duration, error) {
	ns := value * float64(time.Second)
	if math.IsNaN(ns) || math.IsInf(ns, 0) || ns < 0 || ns >= float64(math.MaxInt64) ||
		(value > 0 && ns < 1) {
		return 0, ErrInvalidRequest
	}
	return time.Duration(math.Round(ns)), nil
}

// Start admits a recording of one stream; the requested history must be buffered.
func (s *Service) Start(ctx context.Context, options StartOptions) (model.Recording, error) {
	if options.StreamID == "" || options.StartTS.IsZero() || options.StartTS.After(s.now()) || options.PreEvent < 0 ||
		options.PreEvent > 300*time.Second || (options.Duration != nil && *options.Duration <= 0) {
		return model.Recording{}, ErrInvalidRequest
	}
	start := options.StartTS.Add(-options.PreEvent)
	end := time.Time{}
	if options.Duration != nil {
		end = options.StartTS.Add(*options.Duration)
	}
	if !time.Unix(0, start.UnixNano()).Equal(start) ||
		(!end.IsZero() && !time.Unix(0, end.UnixNano()).Equal(end)) {
		return model.Recording{}, ErrInvalidRequest
	}
	metadata := map[string]any{}
	if options.Metadata != nil {
		encoded, err := json.Marshal(options.Metadata)
		if err != nil {
			return model.Recording{}, ErrInvalidRequest
		}
		if err := json.Unmarshal(encoded, &metadata); err != nil {
			return model.Recording{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return model.Recording{}, ErrConflict
	}
	if len(s.jobs) >= s.max {
		return model.Recording{}, ErrCapacity
	}
	info, err := s.buffers.GetStream(ctx, options.StreamID)
	if err != nil {
		return model.Recording{}, err
	}
	confidenceOK := info.SyncConfidence == model.SyncNTPSynced || (s.allowBestEffort && info.SyncConfidence == model.SyncBestEffort)
	if info.State != "buffering" || !confidenceOK ||
		info.BufferStat.OldestTS.IsZero() || start.Before(info.BufferStat.OldestTS) {
		return model.Recording{}, stream.ErrHistoryUnavailable
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	lease, err := s.buffers.AcquireBuffer(workerCtx, info.StreamID, start, end)
	if err != nil {
		cancel()
		return model.Recording{}, err
	}
	recordingJob := &job{lease: lease, ctx: workerCtx, cancel: cancel, fixed: options.Duration != nil}
	rollback := func(cause error) error {
		recordingJob.cancel()
		cause = errors.Join(cause, recordingJob.lease.Close())
		if recordingJob.record.RecordingPath != "" {
			cause = errors.Join(cause, s.media.DeleteRecordingObjects(context.Background(), recordingJob.id))
		}
		return cause
	}
	if lease.StartTime().IsZero() {
		return model.Recording{}, rollback(stream.ErrHistoryUnavailable)
	}
	recordID, err := uuid.NewRandom()
	if err != nil {
		return model.Recording{}, rollback(err)
	}
	filename, recordingPath, err := s.media.PrepareLiveRecording(ctx, recordID.String())
	if err != nil {
		return model.Recording{}, rollback(err)
	}
	streamID := info.StreamID
	recordingJob.id, recordingJob.filename = recordID.String(), filename
	recordingJob.record = model.Recording{
		RecordingID: recordingJob.id, SensorID: info.SensorID, StreamID: streamID,
		Origin: model.RecordingOriginLive, StartTS: lease.StartTime(),
		RecordingPath: recordingPath, State: model.RecordingStateRecording,
		Container: "mpegts", CreationTS: s.now().UTC(), Metadata: metadata,
	}
	if !end.IsZero() {
		target := end.UTC()
		recordingJob.record.EndTS = &target
	}
	record, err := s.metadata.CreateMetadata(ctx, recordingJob.record)
	if err != nil {
		return model.Recording{}, rollback(err)
	}
	recordingJob.record = record
	s.jobs[recordingJob.record.RecordingID] = recordingJob
	s.wg.Go(func() { s.run(recordingJob) })
	return recordingJob.record, nil
}

func (s *Service) Stop(ctx context.Context, id string) (model.Recording, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.metadata.GetMetadataByRecordingID(ctx, id)
	if err != nil {
		return rec, false, err
	}
	if rec.State == "ready" {
		return rec, false, nil
	}
	recordingJob := s.jobs[id]
	if recordingJob == nil || recordingJob.fixed || rec.State == "failed" {
		return rec, false, ErrConflict
	}
	if recordingJob.stopped {
		return rec, true, nil
	}
	target := s.now().UTC()
	if err := recordingJob.lease.SetEnd(target); err != nil {
		return rec, false, err
	}
	rec.State, rec.EndTS = "finalizing", &target
	rec, err = s.metadata.UpdateMetadata(ctx, id, rec)
	if err != nil {
		recordingJob.cancel()
		return rec, false, err
	}
	recordingJob.record, recordingJob.stopped = rec, true
	return rec, true, nil
}

func (s *Service) Get(ctx context.Context, id string) (model.Recording, error) {
	return s.metadata.GetMetadataByRecordingID(ctx, id)
}

func (s *Service) List(ctx context.Context, filter model.RecordingFilter) ([]model.Recording, string, error) {
	return s.metadata.ListMetadata(ctx, filter)
}

func (s *Service) DeleteRecording(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.metadata.GetMetadataByRecordingID(ctx, id)
	if err != nil {
		return err
	}
	if rec.State == "recording" || rec.State == "finalizing" || s.jobs[id] != nil {
		return ErrConflict
	}
	if err := s.media.DeleteRecordingObjects(ctx, id); err != nil {
		return fmt.Errorf("%w: %w", ErrCleanup, err)
	}
	if err := s.metadata.DeleteMetadata(ctx, id); err != nil {
		return fmt.Errorf("%w: %w", ErrCleanup, err)
	}
	return nil
}

func (s *Service) run(recordingJob *job) {
	defer recordingJob.cancel()
	start, end, size, codec, err := s.writeMedia(recordingJob)
	err = errors.Join(err, recordingJob.lease.Close())
	s.mu.Lock()
	defer s.mu.Unlock()
	defer delete(s.jobs, recordingJob.record.RecordingID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err == nil {
		recordingJob.record.StartTS, recordingJob.record.EndTS = start, &end
		recordingJob.record.State, recordingJob.record.SizeBytes, recordingJob.record.Codec = model.RecordingStateReady, size, codec
		_, err = s.metadata.UpdateMetadata(ctx, recordingJob.record.RecordingID, recordingJob.record)
	}
	if err != nil {
		detail := "recording could not be completed"
		if errors.Is(err, stream.ErrSliceExpired) {
			detail = "a required buffer slice exceeded its retention grace period"
		} else if errors.Is(err, stream.ErrSourceFailed) {
			detail = "recording source became unavailable"
		} else if recordingJob.ctx.Err() != nil {
			detail = "recording interrupted before completion"
		}
		recordingJob.record.State, recordingJob.record.SizeBytes, recordingJob.record.ErrorDetails = model.RecordingStateFailed, 0, detail
		_, saveErr := s.metadata.UpdateMetadata(ctx, recordingJob.record.RecordingID, recordingJob.record)
		cleanupErr := s.media.DeleteRecordingObjects(ctx, recordingJob.record.RecordingID)
		recordingJob.err = errors.Join(saveErr, cleanupErr)
		s.closeErr = errors.Join(s.closeErr, recordingJob.err)
		log.Printf("recording %s failed: %v", recordingJob.record.RecordingID, errors.Join(err, recordingJob.err))
	}
}

func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		for _, recordingJob := range s.jobs {
			recordingJob.cancel()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return s.closeErr
}
