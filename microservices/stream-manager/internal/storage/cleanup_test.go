// SPDX-FileCopyrightText: (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestDeleteSourceMediaEligibility(t *testing.T) {
	_, media, _ := testStores(t)
	ctx := context.Background()
	filename, uri, release, err := media.PrepareMediaWithLease(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	called := false
	eligible := func() (bool, error) {
		called = true
		if media.mu.TryLock() {
			media.mu.Unlock()
			t.Error("eligibility check did not hold the lease mutex")
		}
		return true, nil
	}
	if deleted, err := media.DeleteSourceMediaIf(ctx, uri, eligible); deleted || !errors.Is(err, ErrMediaBusy) || called {
		t.Fatalf("active recording eligibility: deleted=%v error=%v checked=%v", deleted, err, called)
	}
	release()
	lookupErr := errors.New("metadata unavailable")
	for _, tc := range []struct {
		name    string
		allowed bool
		err     error
	}{
		{name: "unexpired"},
		{name: "lookup failure", err: lookupErr},
		{name: "eligible", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleted, err := media.DeleteSourceMediaIf(ctx, uri, func() (bool, error) {
				_, _ = eligible()
				return tc.allowed, tc.err
			})
			if deleted != tc.allowed || !errors.Is(err, tc.err) {
				t.Fatalf("deleted=%v error=%v", deleted, err)
			}
			_, statErr := os.Stat(filepath.Dir(filename))
			if tc.allowed {
				if !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("eligible directory remained: %v", statErr)
				}
			} else if statErr != nil {
				t.Fatalf("ineligible directory removed: %v", statErr)
			}
		})
	}
	if !called {
		t.Fatal("eligibility was not checked")
	}
}

func TestDeleteMediaByID(t *testing.T) {
	_, media, _ := testStores(t)
	ctx := context.Background()
	rec := testRecording(t, media)
	_, _, err := media.PutDerived(ctx, rec.RecordingPath, "frame-0.png", func(path string) error {
		return os.WriteFile(path, []byte("derived"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "../" + rec.RecordingID, rec.RecordingID + "/derived", uuid.NewString()} {
		if err := media.DeleteMediaByID(ctx, id); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid or missing ID %q: %v", id, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := media.DeleteMediaByID(cancelled, rec.RecordingID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled deletion: %v", err)
	}
	release, err := media.AcquireMedia(ctx, rec.RecordingPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := media.DeleteMediaByID(ctx, rec.RecordingID); !errors.Is(err, ErrMediaBusy) {
		t.Fatalf("leased directory was not protected: %v", err)
	}
	release()
	if err := media.DeleteMediaByID(ctx, rec.RecordingID); err != nil {
		t.Fatal(err)
	}
	if got := media.UsedBytes(); got != 0 {
		t.Fatalf("derived quota was not released: %d", got)
	}
	if err := media.DeleteMediaByID(ctx, rec.RecordingID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repeated ID deletion should report not found: %v", err)
	}
	if err := media.DeleteMedia(ctx, rec.RecordingPath); err != nil {
		t.Fatalf("URI deletion is no longer idempotent: %v", err)
	}
	rec = testRecording(t, media)
	if err := media.ReserveMedia(ctx, rec.RecordingPath, 6); err != nil {
		t.Fatal(err)
	}
	if err := media.root.Remove(rec.RecordingID); err != nil {
		t.Fatal(err)
	}
	if err := media.DeleteMedia(ctx, rec.RecordingPath); err != nil {
		t.Fatal(err)
	}
	if got := media.UsedBytes(); got != 0 {
		t.Fatalf("missing media kept reserved quota: %d", got)
	}
}

func TestPrepareMediaLease(t *testing.T) {
	ctx := context.Background()
	media, err := OpenMediaStore(filepath.Join(t.TempDir(), "storage"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = media.Close() }()
	filename, uri, release, err := media.PrepareMediaWithLease(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := media.DeleteSourceMedia(ctx, uri); !errors.Is(err, ErrMediaBusy) {
		t.Fatalf("new recording directory was not protected: %v", err)
	}
	if err := os.WriteFile(filename, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := media.PutMedia(ctx, uri); err != nil {
		t.Fatal(err)
	}
	if err := media.DeleteMedia(ctx, uri); !errors.Is(err, ErrMediaBusy) {
		t.Fatalf("publishing media released the lease before metadata finalization: %v", err)
	}
	release()
	release()
	if err := media.DeleteSourceMedia(ctx, uri); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(filename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source-only recording directory remained: %v", err)
	}
	if got := media.UsedBytes(); got != 0 {
		t.Fatalf("source quota not reclaimed: %d", got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, release, err := media.PrepareMediaWithLease(cancelled, uuid.NewString()); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatalf("cancelled preparation: release=%v error=%v", release != nil, err)
	}
}
