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
