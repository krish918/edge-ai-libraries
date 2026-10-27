// SPDX-FileCopyrightText: (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package cleanup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/model"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/retrieve"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/storage"
)

func TestSweepSummary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  string
		level   string
		deleted int
		busy    int
		failed  int
	}{
		{name: "empty", status: "completed", level: "INFO"},
		{name: "deletions", status: "completed", level: "INFO", deleted: 2},
		{name: "busy", status: "completed", level: "INFO", busy: 2},
		{name: "media failure", status: "failed", level: "ERROR", failed: 2},
		{name: "metadata failure", status: "failed", level: "ERROR", failed: 1},
		{name: "cancelled", status: "cancelled", level: "INFO"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			ctx := context.Background()
			now := time.Now()
			switch tc.name {
			case "deletions":
				rec, _ := e.seed(t, &now)
				e.derived(t, rec, now)
				orphan, _ := e.seed(t, nil)
				if err := e.metadata.DeleteMetadata(ctx, orphan.RecordingID); err != nil {
					t.Fatal(err)
				}
				e.seed(t, nil)
			case "busy":
				rec, _ := e.seed(t, &now)
				release, err := e.media.AcquireMedia(ctx, rec.RecordingPath)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			case "media failure":
				e.seed(t, &now)
				e.media.deleteErr = errors.New("disk unavailable")
			case "metadata failure":
				e.seed(t, &now)
				e.metadata.deleteErr = errors.New("database unavailable")
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}

			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			defer slog.SetDefault(previous)

			err := e.service.sweep(ctx, now)
			if (err != nil) != (tc.failed > 0 || tc.status == "cancelled") {
				t.Fatalf("unexpected sweep error: %v", err)
			}
			type logSummary struct {
				Message  string `json:"msg"`
				Level    string `json:"level"`
				Status   string `json:"status"`
				Duration *int64 `json:"duration"`
				Deleted  int    `json:"deleted_recordings"`
				Busy     int    `json:"busy_skips"`
				Failed   int    `json:"failed_operations"`
				Error    string `json:"error"`
			}
			var summary logSummary
			decoder := json.NewDecoder(&output)
			summaries := 0
			for {
				var entry logSummary
				if err := decoder.Decode(&entry); errors.Is(err, io.EOF) {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if entry.Message == "media cleanup summary" {
					summary = entry
					summaries++
				}
			}
			if summaries != 1 {
				t.Fatalf("expected exactly one summary log, got %d", summaries)
			}
			if summary.Message != "media cleanup summary" || summary.Level != tc.level ||
				summary.Status != tc.status || summary.Deleted != tc.deleted ||
				summary.Busy != tc.busy || summary.Failed != tc.failed {
				t.Fatalf("unexpected summary: %+v", summary)
			}
			if summary.Duration == nil || *summary.Duration < 0 {
				t.Fatalf("invalid duration: %v", summary.Duration)
			}
			if (summary.Error != "") != (err != nil) {
				t.Fatalf("summary error %q does not match sweep error %v", summary.Error, err)
			}
		})
	}
}

type testMedia struct {
	*storage.MediaStore
	deleteErr    error
	beforeDelete func()
}

func (m *testMedia) DeleteMediaIfEligible(ctx context.Context, uri string, eligible func() (bool, error)) (bool, error) {
	if m.beforeDelete != nil {
		m.beforeDelete()
	}
	if m.deleteErr != nil {
		return false, m.deleteErr
	}
	return m.MediaStore.DeleteMediaIfEligible(ctx, uri, eligible)
}

type testMetadata struct {
	*storage.MetadataStore
	deleteErr error
	readErr   error
}

func (m *testMetadata) DeleteMetadata(ctx context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	return m.MetadataStore.DeleteMetadata(ctx, id)
}

func (m *testMetadata) GetMetadataByRecordingID(ctx context.Context, id string) (model.Recording, error) {
	if m.readErr != nil {
		return model.Recording{}, m.readErr
	}
	return m.MetadataStore.GetMetadataByRecordingID(ctx, id)
}

type testEnv struct {
	service  *Service
	metadata *testMetadata
	media    *testMedia
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "storage")
	metadata, err := storage.OpenMetadataStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = metadata.Close() })
	media, err := storage.OpenMediaStore(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = media.Close() })
	e := testEnv{metadata: &testMetadata{MetadataStore: metadata}, media: &testMedia{MediaStore: media}}
	e.service = &Service{metadata: e.metadata, media: e.media}
	return e
}

func (e testEnv) seed(t *testing.T, expiry *time.Time) (model.Recording, string) {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	file, uri, err := e.media.PrepareMedia(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.media.PutMedia(ctx, uri); err != nil {
		t.Fatal(err)
	}
	end := time.Now()
	rec, err := e.metadata.CreateMetadata(ctx, model.Recording{
		RecordingID: id, SensorID: "cleanup-test", State: "ready", RecordingPath: uri,
		StartTS: end.Add(-time.Minute), EndTS: &end, CreationTS: end, ExpiryTS: expiry,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec, file
}

func (e testEnv) derived(t *testing.T, rec model.Recording, mtime time.Time) string {
	t.Helper()
	path, _, err := e.media.PutDerived(context.Background(), rec.RecordingPath, "frame-0.png", func(path string) error {
		return os.WriteFile(path, []byte("derived"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func (e testEnv) assertDerivedAvailable(t *testing.T, id string) {
	t.Helper()
	svc, err := retrieve.NewService(e.metadata, e.media)
	if err != nil {
		t.Fatal(err)
	}
	file, info, err := svc.OpenMedia(context.Background(), id, "frame-0.png")
	if err != nil {
		t.Fatalf("derived URL is no longer available: %v", err)
	}
	data, readErr := io.ReadAll(file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	if string(data) != "derived" || info.ExpiryTS != nil {
		t.Fatalf("derived content or lifetime changed: %q %+v", data, info)
	}
}

func TestCleanupProtectsRecordingCreation(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	now := time.Now()
	filename, uri, release, err := e.media.PrepareMediaWithLease(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := e.service.sweep(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(filename)); err != nil {
		t.Fatalf("creation gap was mistaken for an orphan: %v", err)
	}
	if err := os.WriteFile(filename, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.media.PutMedia(ctx, uri); err != nil {
		t.Fatal(err)
	}
	if err := e.service.sweep(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filename); err != nil {
		t.Fatalf("unfinished recording source was deleted: %v", err)
	}
	release()
	if err := e.service.sweep(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(filename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned recording directory was not cleaned: %v", err)
	}
	for _, state := range []string{"recording", "finalizing"} {
		rec, file := e.seed(t, &now)
		rec.State = state
		if _, err := e.metadata.UpdateMetadata(ctx, rec.RecordingID, rec); err != nil {
			t.Fatal(err)
		}
		if err := e.service.sweep(ctx, now); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("active %s recording was removed: %v", state, err)
		}
	}
}

func TestLegacyDerivedOrphanRemovedAfterRestart(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	now := time.Now()
	rec, filename := e.seed(t, nil)
	e.derived(t, rec, now.Add(-48*time.Hour))
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := e.metadata.DeleteMetadata(ctx, rec.RecordingID); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(e.media.Close(), e.metadata.Close()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(filepath.Dir(filename))
	metadata, err := storage.OpenMetadataStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = metadata.Close() }()
	media, err := storage.OpenMediaStore(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = media.Close() }()
	reopened := testEnv{
		metadata: &testMetadata{MetadataStore: metadata},
		media:    &testMedia{MediaStore: media},
		service:  &Service{metadata: metadata, media: media},
	}
	if err := reopened.service.sweep(ctx, now.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(filename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy derived-only orphan remained: %v", err)
	}
	if got := media.UsedBytes(); got != 0 {
		t.Fatalf("legacy derived quota was not reclaimed: %d", got)
	}
}

func TestCleanupRechecksOrphanCandidate(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	now := time.Now()
	id := uuid.NewString()
	filename, uri, release, err := e.media.PrepareMediaWithLease(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	e.media.beforeDelete = func() {
		e.media.beforeDelete = nil
		if err := os.WriteFile(filename, []byte("source"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.media.PutMedia(ctx, uri); err != nil {
			t.Fatal(err)
		}
		future := now.Add(time.Hour)
		_, err := e.metadata.CreateMetadata(ctx, model.Recording{
			RecordingID: id, SensorID: "cleanup-test", State: "ready",
			RecordingPath: uri, StartTS: now.Add(-time.Minute),
			EndTS: &now, CreationTS: now, ExpiryTS: &future,
		})
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	if err := e.service.sweep(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filename); err != nil {
		t.Fatalf("newly completed recording source was deleted: %v", err)
	}
	if _, err := e.metadata.GetMetadataByRecordingID(ctx, id); err != nil {
		t.Fatalf("newly completed recording metadata was deleted: %v", err)
	}
}

func TestCleanupExpiryAndQuota(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	now := time.Now()
	var expired []model.Recording
	for range batchSize + 5 {
		rec, _ := e.seed(t, &now)
		e.derived(t, rec, now)
		expired = append(expired, rec)
	}
	future := now.Add(time.Hour)
	kept, keptFile := e.seed(t, &future)
	staleDerived := e.derived(t, kept, now.Add(-time.Hour))
	forever, foreverFile := e.seed(t, nil)
	freshDerived := e.derived(t, forever, now)
	orphan, orphanFile := e.seed(t, nil)
	e.derived(t, orphan, now)
	if err := e.metadata.DeleteMetadata(ctx, orphan.RecordingID); err != nil {
		t.Fatal(err)
	}
	if err := e.service.sweep(ctx, now); err != nil {
		t.Fatal(err)
	}
	for _, rec := range expired {
		if _, err := e.metadata.GetMetadataByRecordingID(ctx, rec.RecordingID); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("expired row remained: %v", err)
		}
		if _, err := e.media.OpenMedia(ctx, rec.RecordingPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired source remained: %v", err)
		}
		if _, err := e.media.OpenDerived(ctx, rec.RecordingPath, "frame-0.png"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired derived media remained: %v", err)
		}
	}
	for _, path := range []string{keptFile, foreverFile, freshDerived, staleDerived} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unexpired media removed: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Dir(orphanFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan directory remained: %v", err)
	}
	wantUsage := 2 * int64(len("source")+len("derived"))
	if got := e.media.UsedBytes(); got != wantUsage {
		t.Fatalf("expired and orphan quota not reclaimed: got %d, want %d", got, wantUsage)
	}
	if err := e.service.sweep(ctx, now.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.assertDerivedAvailable(t, forever.RecordingID)
	if got := e.media.UsedBytes(); got != int64(len("source")+len("derived")) {
		t.Fatalf("unexpired quota changed: %d", got)
	}
}

func TestCleanupRetries(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	now := time.Now()
	expired, filename := e.seed(t, &now)
	derived := e.derived(t, expired, now)
	forever, _ := e.seed(t, nil)
	keptDerived := e.derived(t, forever, now.Add(-48*time.Hour))
	release, err := e.media.AcquireMedia(ctx, expired.RecordingPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.service.sweep(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.metadata.GetMetadataByRecordingID(ctx, expired.RecordingID); err != nil {
		t.Fatalf("busy recording metadata removed: %v", err)
	}
	release()
	e.media.deleteErr = errors.New("media unavailable")
	if err := e.service.sweep(ctx, now); !errors.Is(err, e.media.deleteErr) {
		t.Fatalf("media failure not reported: %v", err)
	}
	for _, path := range []string{filename, derived} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.metadata.GetMetadataByRecordingID(ctx, expired.RecordingID); err != nil {
		t.Fatalf("metadata lost before retry: %v", err)
	}
	e.media.deleteErr = nil
	e.metadata.deleteErr = errors.New("metadata unavailable")
	if err := e.service.sweep(ctx, now); !errors.Is(err, e.metadata.deleteErr) {
		t.Fatalf("metadata failure not reported: %v", err)
	}
	e.metadata.deleteErr = nil
	if err := e.service.sweep(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.metadata.GetMetadataByRecordingID(ctx, expired.RecordingID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expired row not retried after media removal: %v", err)
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired source remained: %v", err)
	}
	if _, err := os.Stat(derived); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired derived media remained: %v", err)
	}
	if _, err := os.Stat(keptDerived); err != nil {
		t.Fatalf("old derived media was removed: %v", err)
	}
}

func TestOrphanCleanupFailsClosed(t *testing.T) {
	e := newTestEnv(t)
	rec, filename := e.seed(t, nil)
	e.metadata.readErr = errors.New("metadata unavailable")
	if err := e.service.sweep(context.Background(), time.Now()); !errors.Is(err, e.metadata.readErr) {
		t.Fatalf("lookup error not reported: %v", err)
	}
	if _, err := os.Stat(filename); err != nil {
		t.Fatalf("lookup failure deleted media: %v", err)
	}
	e.metadata.readErr = nil
	if _, err := e.metadata.GetMetadataByRecordingID(context.Background(), rec.RecordingID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.service.sweep(ctx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not reported: %v", err)
	}
}

func TestStartupCleanup(t *testing.T) {
	e := newTestEnv(t)
	past := time.Now().Add(-time.Minute)
	rec, filename := e.seed(t, &past)
	cleaner := NewService(e.metadata, e.media)
	t.Cleanup(func() { _ = cleaner.Close() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := e.metadata.GetMetadataByRecordingID(context.Background(), rec.RecordingID)
		if errors.Is(err, storage.ErrNotFound) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("startup cleanup did not delete expired recording: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := os.Stat(filepath.Dir(filename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup cleanup kept media: %v", err)
	}
}

type emptyMetadata struct {
	storage.MetadataHandler
	calls atomic.Int64
}

func (m *emptyMetadata) ListMetadata(context.Context, model.RecordingFilter) ([]model.Recording, string, error) {
	m.calls.Add(1)
	return nil, "", nil
}

type emptyMedia struct{ storage.MediaHandler }

func (emptyMedia) ListMedia(context.Context) (map[string]string, error) { return nil, nil }

func TestDailyScheduleAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		metadata := &emptyMetadata{}
		svc := NewService(metadata, emptyMedia{})

		defer func() { _ = svc.Close() }()
		synctest.Wait()
		if got := metadata.calls.Load(); got != 1 {
			t.Fatalf("startup sweeps = %d, want 1", got)
		}
		time.Sleep(24*time.Hour - time.Nanosecond)
		synctest.Wait()
		if got := metadata.calls.Load(); got != 1 {
			t.Fatalf("cleanup ran before 24 hours: %d", got)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := metadata.calls.Load(); got != 2 {
			t.Fatalf("daily sweeps = %d, want 2", got)
		}
		_ = svc.Close()
		time.Sleep(24 * time.Hour)
		if got := metadata.calls.Load(); got != 2 {
			t.Fatalf("cleanup continued after shutdown: %d", got)
		}
	})
}
