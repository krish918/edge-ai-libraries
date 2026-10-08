// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/sys/unix"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/model"
)

// recordingsSchema is the authoritative table definition. It is executed
// verbatim rather than derived from struct tags via AutoMigrate, so the
// CHECK constraints and index are reviewable in one place and cannot drift
// as the Go struct evolves.
const recordingsSchema = `
CREATE TABLE IF NOT EXISTS recordings (
	recording_id   TEXT PRIMARY KEY,
	sensor_id      TEXT NOT NULL,
	stream_id      TEXT,
	origin         TEXT NOT NULL CHECK (origin IN ('live', 'import')),
	state          TEXT NOT NULL CHECK (state IN ('recording', 'finalizing', 'ready', 'failed')),
	start_ts       INTEGER NOT NULL,
	end_ts         INTEGER CHECK (end_ts IS NULL OR end_ts > start_ts),
	recording_path TEXT NOT NULL,
	codec          TEXT,
	container      TEXT,
	size_bytes     INTEGER CHECK (size_bytes IS NULL OR size_bytes >= 0),
	metadata       TEXT NOT NULL DEFAULT '{}',
	creation_ts    INTEGER NOT NULL,
	expiry_ts      INTEGER,
	error_details  TEXT
);
CREATE INDEX IF NOT EXISTS idx_recordings_sensor_stream ON recordings(sensor_id, stream_id);
`

// recordingRow is the persistence representation of model.Recording. It is
// deliberately private and separate from the domain model so GORM column
// tags never leak into the API surface, and so wall-clock instants can be
// stored as Unix nanoseconds without that encoding escaping this package.
type recordingRow struct {
	RecordingID   string  `gorm:"column:recording_id;primaryKey"`
	SensorID      string  `gorm:"column:sensor_id"`
	StreamID      *string `gorm:"column:stream_id"`
	Origin        string  `gorm:"column:origin"`
	State         string  `gorm:"column:state"`
	StartTS       int64   `gorm:"column:start_ts"`
	EndTS         *int64  `gorm:"column:end_ts"`
	RecordingPath string  `gorm:"column:recording_path"`
	Codec         *string `gorm:"column:codec"`
	Container     *string `gorm:"column:container"`
	SizeBytes     *int64  `gorm:"column:size_bytes"`
	Metadata      string  `gorm:"column:metadata"`
	CreationTS    int64   `gorm:"column:creation_ts"`
	ExpiryTS      *int64  `gorm:"column:expiry_ts"`
	ErrorDetails  *string `gorm:"column:error_details"`
}

// TableName pins the table GORM reads and writes, independent of its default
// pluralisation rules.
func (recordingRow) TableName() string { return "recordings" }

// recordingColumns lists every writable column. Passing it to Select forces
// full-field writes so a zero value (an empty codec, a zero size) is written
// explicitly instead of being silently omitted.
var recordingColumns = []string{
	"recording_id", "sensor_id", "stream_id", "origin", "state", "start_ts",
	"end_ts", "recording_path", "codec", "container", "size_bytes",
	"metadata", "creation_ts", "expiry_ts", "error_details",
}

// SQLiteMetadataStore is the production RecordingMetadataStore
// implementation, backed by a single-writer SQLite database in WAL mode
// through a pure-Go driver (no cgo). It never holds a transaction open
// across storage or media operations; every method here is a single
// short-lived statement.
type SQLiteMetadataStore struct {
	db        *gorm.DB
	lock      *os.File
	closeOnce sync.Once
	closeErr  error
}

var _ RecordingLifecycleStore = (*SQLiteMetadataStore)(nil)

// OpenSQLiteMetadataStore opens and initializes the database at dsn. File-backed
// databases use private permissions, a process lock, WAL, and a 5-second timeout.
func OpenSQLiteMetadataStore(ctx context.Context, dsn string) (*SQLiteMetadataStore, error) {
	lock, err := acquireSQLiteLock(dsn)
	if err != nil {
		return nil, fmt.Errorf("lock sqlite database: %w", err)
	}
	keepLock := false
	defer func() {
		if !keepLock {
			_ = releaseSQLiteLock(lock)
		}
	}()

	dsn, err = sqliteDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("prepare sqlite DSN: %w", err)
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{
			LogLevel: logger.Warn, ParameterizedQueries: true, IgnoreRecordNotFoundError: true,
			SlowThreshold: time.Second,
		}),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("access sqlite connection pool: %w", err)
	}
	// A single writer keeps SQLite's write path simple; WAL mode lets reads
	// proceed concurrently with a writer, and the busy timeout bounds how
	// long a caller waits behind a competing write instead of failing
	// immediately with SQLITE_BUSY.
	sqlDB.SetMaxOpenConns(1)

	for _, stmt := range splitStatements(recordingsSchema) {
		if err := db.WithContext(ctx).Exec(stmt).Error; err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("apply recordings schema: %w", err)
		}
	}

	keepLock = true
	return &SQLiteMetadataStore{db: db, lock: lock}, nil
}

// acquireSQLiteLock protects a file-backed database from concurrent service processes.
func acquireSQLiteLock(dsn string) (*os.File, error) {
	databasePath, isFile, err := sqliteDatabasePath(dsn)
	if err != nil || !isFile {
		return nil, err
	}
	lock, err := openPrivateSQLiteFile(databasePath + ".lock")
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("sqlite database is already in use by another process")
		}
		return nil, err
	}

	database, err := openPrivateSQLiteFile(databasePath)
	if err != nil {
		_ = releaseSQLiteLock(lock)
		return nil, err
	}
	if err := database.Close(); err != nil {
		_ = releaseSQLiteLock(lock)
		return nil, err
	}
	return lock, nil
}

// sqliteDatabasePath extracts the file path, returning false for in-memory databases.
func sqliteDatabasePath(dsn string) (string, bool, error) {
	if dsn == ":memory:" {
		return "", false, nil
	}
	if !strings.HasPrefix(dsn, "file:") {
		if dsn == "" {
			return "", false, errors.New("sqlite database path is empty")
		}
		return dsn, true, nil
	}

	databaseURL, err := url.Parse(dsn)
	if err != nil {
		return "", false, err
	}
	if databaseURL.Opaque == ":memory:" || databaseURL.Query().Get("mode") == "memory" {
		return "", false, nil
	}
	if databaseURL.Host != "" {
		return "", false, errors.New("sqlite file DSN must not include a host")
	}
	databasePath := databaseURL.Path
	if databaseURL.Opaque != "" {
		databasePath = databaseURL.Opaque
	}
	if databasePath == "" {
		return "", false, errors.New("sqlite file DSN path is empty")
	}
	return databasePath, true, nil
}

// openPrivateSQLiteFile rejects symlinks and ensures the file is mode 0600.
func openPrivateSQLiteFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("sqlite path %q is not a regular file", path)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// releaseSQLiteLock unlocks and closes a database lock file.
func releaseSQLiteLock(lock *os.File) error {
	if lock == nil {
		return nil
	}
	return errors.Join(unix.Flock(int(lock.Fd()), unix.LOCK_UN), lock.Close())
}

// sqliteDSN adds connection pragmas while preserving file and in-memory DSNs.
func sqliteDSN(dsn string) (string, error) {
	var databaseURL *url.URL
	if strings.HasPrefix(dsn, "file:") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		databaseURL = parsed
	} else {
		databaseURL = &url.URL{Scheme: "file", Path: dsn}
	}

	query := databaseURL.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(1)")
	databaseURL.RawQuery = query.Encode()
	return databaseURL.String(), nil
}

func splitStatements(script string) []string {
	parts := strings.Split(script, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			statements = append(statements, trimmed)
		}
	}
	return statements
}

func (s *SQLiteMetadataStore) Close() error {
	s.closeOnce.Do(func() {
		sqlDB, err := s.db.DB()
		if err != nil {
			s.closeErr = err
		} else {
			s.closeErr = sqlDB.Close()
		}
		s.closeErr = errors.Join(s.closeErr, releaseSQLiteLock(s.lock))
	})
	return s.closeErr
}

func (s *SQLiteMetadataStore) Health(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// GetByID implements RecordingMetadataStore.
func (s *SQLiteMetadataStore) GetByID(ctx context.Context, recordingID string) (model.Recording, error) {
	if err := ValidateIdentifier("recording_id", recordingID); err != nil {
		return model.Recording{}, err
	}

	var row recordingRow
	err := s.db.WithContext(ctx).
		Where("recording_id = ?", recordingID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Recording{}, fmt.Errorf("%w: %q", ErrRecordingNotFound, recordingID)
	}
	if err != nil {
		return model.Recording{}, fmt.Errorf("query recording %q: %w", recordingID, err)
	}

	return rowToRecording(row)
}

// Exists implements RecordingMetadataStore.
func (s *SQLiteMetadataStore) Exists(ctx context.Context, recordingID string) (bool, error) {
	if err := ValidateIdentifier("recording_id", recordingID); err != nil {
		return false, err
	}

	var count int64
	if err := s.db.WithContext(ctx).
		Model(&recordingRow{}).
		Where("recording_id = ?", recordingID).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check recording %q: %w", recordingID, err)
	}
	return count > 0, nil
}

// Save inserts or replaces a recording row. It is used to publish recording
// metadata (and by development seeding); retrieval itself never writes
// through this store.
func (s *SQLiteMetadataStore) Save(ctx context.Context, recording model.Recording) error {
	row, err := recordingToRow(recording)
	if err != nil {
		return err
	}

	err = s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "recording_id"}},
			DoUpdates: clause.AssignmentColumns(recordingColumns[1:]),
		}).
		Select(recordingColumns).
		Create(&row).Error
	if err != nil {
		return fmt.Errorf("save recording %q: %w", recording.RecordingID, err)
	}
	return nil
}

func (s *SQLiteMetadataStore) CreateMetadata(ctx context.Context, recording model.Recording) (model.Recording, error) {
	row, err := recordingToRow(recording)
	if err != nil {
		return model.Recording{}, err
	}
	if err := s.db.WithContext(ctx).Select(recordingColumns).Create(&row).Error; err != nil {
		return model.Recording{}, fmt.Errorf("create recording %q: %w", recording.RecordingID, err)
	}
	return rowToRecording(row)
}

func (s *SQLiteMetadataStore) GetMetadataByRecordingID(ctx context.Context, recordingID string) (model.Recording, error) {
	return s.GetByID(ctx, recordingID)
}

func (s *SQLiteMetadataStore) UpdateMetadata(ctx context.Context, recordingID string, recording model.Recording) (model.Recording, error) {
	if err := ValidateIdentifier("recording_id", recordingID); err != nil {
		return model.Recording{}, err
	}
	recording.RecordingID = recordingID
	row, err := recordingToRow(recording)
	if err != nil {
		return model.Recording{}, err
	}
	result := s.db.WithContext(ctx).Model(&recordingRow{}).
		Where("recording_id = ?", recordingID).
		Select(recordingColumns[1:]).Updates(&row)
	if result.Error != nil {
		return model.Recording{}, fmt.Errorf("update recording %q: %w", recordingID, result.Error)
	}
	if result.RowsAffected == 0 {
		if exists, err := s.Exists(ctx, recordingID); err != nil || !exists {
			if err != nil {
				return model.Recording{}, err
			}
			return model.Recording{}, fmt.Errorf("%w: %q", ErrRecordingNotFound, recordingID)
		}
	}
	return recording, nil
}

func (s *SQLiteMetadataStore) ListMetadata(ctx context.Context, filter model.RecordingFilter) ([]model.Recording, string, error) {
	limit := filter.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 || (!filter.StartTS.IsZero() && !filter.EndTS.IsZero() && !filter.EndTS.After(filter.StartTS)) {
		return nil, "", ErrInvalidRecordingFilter
	}
	query := s.db.WithContext(ctx).Model(&recordingRow{})
	if filter.SensorID != "" {
		query = query.Where("sensor_id = ?", filter.SensorID)
	}
	if filter.StreamID != "" {
		query = query.Where("stream_id = ?", filter.StreamID)
	}
	if filter.State != "" {
		if !validRecordingState(filter.State) {
			return nil, "", ErrInvalidRecordingFilter
		}
		query = query.Where("state = ?", filter.State)
	}
	if !filter.StartTS.IsZero() {
		query = query.Where("(end_ts IS NULL OR end_ts > ?)", filter.StartTS.UTC().UnixNano())
	}
	if !filter.EndTS.IsZero() {
		query = query.Where("start_ts < ?", filter.EndTS.UTC().UnixNano())
	}
	if !filter.ExpiryTS.IsZero() {
		query = query.Where("expiry_ts <= ?", filter.ExpiryTS.UTC().UnixNano())
	}
	for key, value := range filter.Metadata {
		switch value.(type) {
		case nil:
			query = query.Where("EXISTS (SELECT 1 FROM json_each(recordings.metadata) WHERE key = ? AND type = 'null')", key)
		case string, bool, int, int64, float64:
			query = query.Where("EXISTS (SELECT 1 FROM json_each(recordings.metadata) WHERE key = ? AND value = ?)", key, value)
		default:
			return nil, "", ErrInvalidRecordingFilter
		}
	}
	if filter.Cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || len(filter.Cursor) > 256 {
			return nil, "", ErrInvalidRecordingFilter
		}
		timestamp, recordingID, ok := strings.Cut(string(decoded), "\n")
		ns, parseErr := strconv.ParseInt(timestamp, 10, 64)
		if !ok || parseErr != nil || ValidateIdentifier("recording_id", recordingID) != nil {
			return nil, "", ErrInvalidRecordingFilter
		}
		query = query.Where("(creation_ts < ? OR (creation_ts = ? AND recording_id > ?))", ns, ns, recordingID)
	}
	var rows []recordingRow
	if err := query.Order("creation_ts DESC, recording_id ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", fmt.Errorf("list recordings: %w", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(last.CreationTS, 10) + "\n" + last.RecordingID))
	}
	recordings := make([]model.Recording, 0, len(rows))
	for _, row := range rows {
		recording, err := rowToRecording(row)
		if err != nil {
			return nil, "", err
		}
		recordings = append(recordings, recording)
	}
	return recordings, next, nil
}

func (s *SQLiteMetadataStore) DeleteMetadata(ctx context.Context, recordingID string) error {
	if err := ValidateIdentifier("recording_id", recordingID); err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Where("recording_id = ?", recordingID).Delete(&recordingRow{})
	if result.Error != nil {
		return fmt.Errorf("delete recording %q: %w", recordingID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %q", ErrRecordingNotFound, recordingID)
	}
	return nil
}

func validRecordingState(state string) bool {
	switch state {
	case model.RecordingStateRecording, model.RecordingStateFinalizing, model.RecordingStateReady, model.RecordingStateFailed:
		return true
	default:
		return false
	}
}

func recordingToRow(recording model.Recording) (recordingRow, error) {
	if err := ValidateIdentifier("recording_id", recording.RecordingID); err != nil {
		return recordingRow{}, err
	}

	metadata := recording.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return recordingRow{}, fmt.Errorf("marshal metadata for recording %q: %w", recording.RecordingID, err)
	}

	creationTS := recording.CreationTS
	if creationTS.IsZero() {
		creationTS = time.Now().UTC()
	}

	row := recordingRow{
		RecordingID:   recording.RecordingID,
		SensorID:      recording.SensorID,
		StreamID:      nullableString(recording.StreamID),
		Origin:        recording.Origin,
		State:         recording.State,
		StartTS:       recording.StartTS.UTC().UnixNano(),
		RecordingPath: recording.RecordingPath,
		Codec:         nullableString(recording.Codec),
		Container:     nullableString(recording.Container),
		SizeBytes:     &recording.SizeBytes,
		Metadata:      string(metadataJSON),
		CreationTS:    creationTS.UTC().UnixNano(),
		ErrorDetails:  nullableString(recording.ErrorDetails),
	}
	if recording.EndTS != nil {
		endTS := recording.EndTS.UTC().UnixNano()
		row.EndTS = &endTS
	}
	if recording.ExpiryTS != nil {
		expiryTS := recording.ExpiryTS.UTC().UnixNano()
		row.ExpiryTS = &expiryTS
	}
	return row, nil
}

func rowToRecording(row recordingRow) (model.Recording, error) {
	recording := model.Recording{
		RecordingID:   row.RecordingID,
		SensorID:      row.SensorID,
		StreamID:      derefString(row.StreamID),
		Origin:        row.Origin,
		State:         row.State,
		StartTS:       time.Unix(0, row.StartTS).UTC(),
		RecordingPath: row.RecordingPath,
		Codec:         derefString(row.Codec),
		Container:     derefString(row.Container),
		CreationTS:    time.Unix(0, row.CreationTS).UTC(),
		ErrorDetails:  derefString(row.ErrorDetails),
		Metadata:      map[string]any{},
	}
	if row.SizeBytes != nil {
		recording.SizeBytes = *row.SizeBytes
	}
	if row.EndTS != nil {
		endTS := time.Unix(0, *row.EndTS).UTC()
		recording.EndTS = &endTS
	}
	if row.ExpiryTS != nil {
		expiryTS := time.Unix(0, *row.ExpiryTS).UTC()
		recording.ExpiryTS = &expiryTS
	}
	if strings.TrimSpace(row.Metadata) != "" {
		if err := json.Unmarshal([]byte(row.Metadata), &recording.Metadata); err != nil {
			return model.Recording{}, fmt.Errorf("unmarshal metadata for recording %q: %w", row.RecordingID, err)
		}
	}
	return recording, nil
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
