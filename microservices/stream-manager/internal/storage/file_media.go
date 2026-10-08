// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// DerivedURLSigner creates capability URLs for derived media objects.
type DerivedURLSigner interface {
	Sign(key string, expiresAt time.Time) (string, error)
}

// FileMediaStore stores validated logical paths under a private root:
//
//	recordings/{id}/media.{ext}, recordings/{id}/sidecar.{json,jsonl}
//	derived/{id}/frames/... and derived/{id}/clips/...
type FileMediaStore struct {
	root          string
	signer        DerivedURLSigner
	publicBaseURL string
	lock          *os.File
	closeOnce     sync.Once
	closeErr      error
}

var _ LiveRecordingLifecycleMediaStore = (*FileMediaStore)(nil)
var _ MediaStore = (*FileMediaStore)(nil)

// NewFileMediaStore builds a FileMediaStore rooted at root, issuing
// capability URLs of the form "{publicBaseURL}/v1/media/{token}" signed by
// signer. It creates root if it does not already exist.
func NewFileMediaStore(root, publicBaseURL string, signer DerivedURLSigner) (*FileMediaStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("filesystem: root is required")
	}
	if strings.TrimSpace(publicBaseURL) == "" {
		return nil, errors.New("filesystem: public base URL is required")
	}
	if signer == nil {
		return nil, errors.New("filesystem: signer is required")
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("filesystem: resolve root %q: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("filesystem: create root %q: %w", abs, err)
	}
	if err := secureMediaRoot(abs); err != nil {
		return nil, err
	}
	lock, err := acquireMediaStoreLock(abs)
	if err != nil {
		return nil, err
	}

	return &FileMediaStore{
		root:          abs,
		signer:        signer,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		lock:          lock,
	}, nil
}

// acquireMediaStoreLock prevents multiple processes from writing the same media root.
func acquireMediaStoreLock(root string) (*os.File, error) {
	path := filepath.Join(root, ".stream-manager.lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("filesystem: open media lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), path)
	info, err := lock.Stat()
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("filesystem: inspect media lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = lock.Close()
		return nil, errors.New("filesystem: media lock must be a regular file")
	}
	if err := lock.Chmod(0o600); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("filesystem: restrict media lock permissions: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("filesystem media root is already in use by another process")
		}
		return nil, fmt.Errorf("filesystem: lock media root: %w", err)
	}
	return lock, nil
}

// Close releases the lock held on this media root.
func (f *FileMediaStore) Close() error {
	f.closeOnce.Do(func() {
		if f.lock != nil {
			f.closeErr = errors.Join(unix.Flock(int(f.lock.Fd()), unix.LOCK_UN), f.lock.Close())
		}
	})
	return f.closeErr
}

// secureMediaRoot verifies ownership and restricts the media root to mode 0700.
func secureMediaRoot(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("filesystem: open media root securely: %w", err)
	}
	defer unix.Close(fd)

	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		return fmt.Errorf("filesystem: inspect media root: %w", err)
	}
	if info.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("filesystem: media root must be a directory")
	}
	if info.Uid != uint32(os.Geteuid()) {
		return errors.New("filesystem: media root must be owned by the service user")
	}
	if info.Mode&0o077 != 0 {
		if err := unix.Fchmod(fd, 0o700); err != nil {
			return fmt.Errorf("filesystem: restrict media root permissions: %w", err)
		}
	}
	return nil
}

// resolveKey validates a logical object key and converts it to a root-relative path.
func resolveKey(logicalKey string) (string, error) {
	if err := ValidateObjectKey(logicalKey); err != nil {
		return "", err
	}
	return filepath.FromSlash(logicalKey), nil
}

func (f *FileMediaStore) OpenRecording(_ context.Context, recordingPath string) (io.ReadCloser, error) {
	return f.openObject(recordingPath)
}

func (f *FileMediaStore) OpenSidecar(_ context.Context, recordingID string) (io.ReadCloser, error) {
	key, err := RecordingSidecarKey(recordingID)
	if err != nil {
		return nil, err
	}
	return f.openObject(key)
}

func (f *FileMediaStore) OpenSidecarForRecording(_ context.Context, recordingID, recordingPath string) (io.ReadCloser, error) {
	key, err := RecordingSidecarKeyForPath(recordingID, recordingPath)
	if err != nil {
		return nil, err
	}
	return f.openObject(key)
}

func (f *FileMediaStore) OpenDerived(_ context.Context, key string) (io.ReadCloser, error) {
	return f.openObject(key)
}

func (f *FileMediaStore) openObject(logicalKey string) (io.ReadCloser, error) {
	relative, err := resolveKey(logicalKey)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return nil, fmt.Errorf("open filesystem root: %w", err)
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrObjectNotFound, logicalKey)
		}
		return nil, fmt.Errorf("open object %q: %w", logicalKey, err)
	}
	return file, nil
}

func (f *FileMediaStore) PutRecording(_ context.Context, recordingID string, media io.Reader, _ string) (string, error) {
	key, err := RecordingMediaKey(recordingID)
	if err != nil {
		return "", err
	}
	if err := f.putObject(key, media); err != nil {
		return "", err
	}
	return key, nil
}

func (f *FileMediaStore) PutSidecar(_ context.Context, recordingID string, sidecar io.Reader) (string, error) {
	key, err := RecordingSidecarKey(recordingID)
	if err != nil {
		return "", err
	}
	if err := f.putObject(key, sidecar); err != nil {
		return "", err
	}
	return key, nil
}

func (f *FileMediaStore) PutLiveRecording(_ context.Context, recordingID string, media io.Reader) (string, error) {
	key, err := RecordingLiveMediaKey(recordingID)
	if err != nil {
		return "", err
	}
	if err := f.putObject(key, media); err != nil {
		return "", err
	}
	return key, nil
}

func (f *FileMediaStore) PutLiveSidecar(_ context.Context, recordingID string, sidecar io.Reader) (string, error) {
	key, err := RecordingLiveSidecarKey(recordingID)
	if err != nil {
		return "", err
	}
	if err := f.putObject(key, sidecar); err != nil {
		return "", err
	}
	return key, nil
}

func (f *FileMediaStore) PrepareLiveRecording(ctx context.Context, recordingID string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	key, err := RecordingLiveMediaKey(recordingID)
	if err != nil {
		return "", "", err
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	relative := filepath.FromSlash(key)
	if err := root.MkdirAll(filepath.Dir(relative), 0o750); err != nil {
		return "", "", fmt.Errorf("create live recording directory: %w", err)
	}
	file, err := root.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", fmt.Errorf("create live recording: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = root.Remove(relative)
		return "", "", err
	}
	return filepath.Join(f.root, relative), key, nil
}

func (f *FileMediaStore) OpenLiveSidecarWriter(ctx context.Context, recordingID, recordingPath string) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := RecordingSidecarKeyForPath(recordingID, recordingPath)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	relative := filepath.FromSlash(key)
	file, err := root.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create live sidecar: %w", err)
	}
	return &syncingFileWriter{File: file}, nil
}

func (f *FileMediaStore) FinalizeLiveRecording(ctx context.Context, recordingPath string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := ValidateObjectKey(recordingPath); err != nil {
		return 0, err
	}
	if filepath.Base(filepath.FromSlash(recordingPath)) != recordingLiveMediaObject {
		return 0, fmt.Errorf("%w: not a live media path", ErrInvalidObjectKey)
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.FromSlash(recordingPath), os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	info, statErr := file.Stat()
	if statErr == nil && (!info.Mode().IsRegular() || info.Size() <= 0) {
		statErr = errors.New("live recording is empty or not a regular file")
	}
	if err := errors.Join(statErr, file.Sync(), file.Close()); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

type syncingFileWriter struct {
	*os.File
}

func (w *syncingFileWriter) Close() error {
	return errors.Join(w.File.Sync(), w.File.Close())
}

func (f *FileMediaStore) PutDerived(_ context.Context, key string, media io.Reader, _ string, _ time.Duration) error {
	return f.putObject(key, media)
}

// putObject writes body to a temporary file alongside the destination and
// then atomically renames it into place, so a reader can never observe a
// partially written object at the final path.
func (f *FileMediaStore) putObject(logicalKey string, body io.Reader) error {
	relative, err := resolveKey(logicalKey)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return fmt.Errorf("put object %q: open filesystem root: %w", logicalKey, err)
	}
	defer root.Close()
	dir := filepath.Dir(relative)
	if err := root.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("put object %q: create directory: %w", logicalKey, err)
	}

	tmpPath := filepath.Join(dir, ".stage-"+uuid.NewString())
	tmp, err := root.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("put object %q: create staging file: %w", logicalKey, err)
	}
	// Always attempt to remove the staging file; after a successful
	// rename it is already gone, so this is a no-op in the success path.
	defer root.Remove(tmpPath)

	if _, err := io.Copy(tmp, body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("put object %q: write staging file: %w", logicalKey, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("put object %q: sync staging file: %w", logicalKey, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("put object %q: close staging file: %w", logicalKey, err)
	}
	if err := root.Rename(tmpPath, relative); err != nil {
		return fmt.Errorf("put object %q: publish: %w", logicalKey, err)
	}
	return nil
}

func (f *FileMediaStore) DerivedExists(_ context.Context, key string) (bool, error) {
	relative, err := resolveKey(key)
	if err != nil {
		return false, err
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return false, fmt.Errorf("open filesystem root: %w", err)
	}
	defer root.Close()
	info, err := root.Lstat(relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat object %q: %w", key, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("stat object %q: not a regular file", key)
	}
	return true, nil
}

// PresignDerived returns a GET /v1/media/{token} capability URL for key,
// signed to expire after expiresIn. The raw filesystem path is never
// embedded in the token or the URL.
func (f *FileMediaStore) PresignDerived(_ context.Context, key string, expiresIn time.Duration) (string, time.Time, error) {
	if err := ValidateObjectKey(key); err != nil {
		return "", time.Time{}, err
	}
	if expiresIn <= 0 {
		return "", time.Time{}, fmt.Errorf("presign object %q: expiry must be positive", key)
	}

	expiresAt := time.Now().UTC().Add(expiresIn)
	token, err := f.signer.Sign(key, expiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("presign object %q: %w", key, err)
	}
	return f.publicBaseURL + "/v1/media/" + token, expiresAt, nil
}

// DeleteRecordingObjects removes the recording, sidecar, and every derived
// object for recordingID by removing their directories outright.
func (f *FileMediaStore) DeleteRecordingObjects(_ context.Context, recordingID string) error {
	prefixes, err := RecordingObjectPrefixes(recordingID)
	if err != nil {
		return err
	}
	for _, logicalPrefix := range prefixes {
		dirKey := strings.TrimSuffix(logicalPrefix, "/")
		relative, err := resolveKey(dirKey)
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(f.root)
		if err != nil {
			return fmt.Errorf("delete objects under %q: open filesystem root: %w", logicalPrefix, err)
		}
		if err := root.RemoveAll(relative); err != nil {
			_ = root.Close()
			return fmt.Errorf("delete objects under %q: %w", logicalPrefix, err)
		}
		if err := root.Close(); err != nil {
			return fmt.Errorf("delete objects under %q: close filesystem root: %w", logicalPrefix, err)
		}
	}
	return nil
}

// Health confirms root exists and is writable by staging and removing a
// throwaway file inside it.
func (f *FileMediaStore) Health(_ context.Context) error {
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return fmt.Errorf("filesystem root %q unavailable: %w", f.root, err)
	}
	defer root.Close()
	probePath := ".health-" + uuid.NewString()
	probe, err := root.OpenFile(probePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("filesystem root %q not writable: %w", f.root, err)
	}
	if err := probe.Close(); err != nil {
		_ = root.Remove(probePath)
		return fmt.Errorf("filesystem root %q probe close: %w", f.root, err)
	}
	if err := root.Remove(probePath); err != nil {
		return fmt.Errorf("filesystem root %q probe cleanup: %w", f.root, err)
	}
	return nil
}

// InferredDerivedContentType returns a best-effort MIME type for a derived
// object key based on its file extension, for backends (like FileMediaStore)
// that do not separately persist the content type an object was published
// with.
func InferredDerivedContentType(key string) string {
	if ct := mime.TypeByExtension(filepath.Ext(key)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
