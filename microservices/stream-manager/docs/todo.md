<!-- SPDX-FileCopyrightText: (C) 2026 Intel Corporation -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# TODO

Items are listed roughly in order of priority.

## Features

### Major
- [ ] Define a consistent data-directory setup for host and container deployments; media and SQLite paths are currently configured separately
- [x] Implement the `PUT /buffer` endpoint to resize stream buffers
- [ ] Add S3-compatible storage for videos
- [ ] Support live recording writes to S3-compatible storage, including media and sidecar publishing, finalization, recovery, and cleanup
- [ ] Support multi-stream recording start requests
- [ ] Restore automated Go unit and integration test coverage in a follow-up PR; this branch intentionally has no test files

### Incremental
- [ ] Implement support for user supplied config file (located at user home config directory). JSON or YAML preferred.
- [ ] Add pagination to the `GET /streams` endpoint

## Optimization or Improvements

### Major
- [x] Use `log/slog` for structured service and access logging
- [ ] Migrate remaining standard-library log calls to `log/slog` and use request-scoped logging consistently
- [ ] Add a custom error-handling framework using `StreamManError`

### Incremental
- [ ] Enforce a filesystem recording-storage quota from `SM_RECORDING_STORAGE`; the legacy setting is parsed but currently does not limit disk use
- [ ] Use `errors.Join()` to combine `recordingFilter` validation errors and similar errors across endpoints
- [ ] Evaluate whether `DecodeJSON` would be useful as middleware
- [ ] Add deployment helper scripts for adding Stream Manager, with configured data paths, to an existing Docker Compose project

### Research/Exploration
- [ ] Weigh in tradeoffs of using random UUIDs as primary key in the database - should UUIDv7 be used or a shorter UUID or incremental int prefixed to UUIDs.
- [ ] Evaluate S3-compatible storage tradeoffs for video recordings, especially open recordings and real-time ingestion
- [ ] Evaluate whether fMP4 or another format can reduce MPEG-TS storage overhead while retaining its robustness
- [ ] Evaluate WebM instead of MP4 as the default Replay API clip format to use fully open-source codecs

## Bugs/Issues

- [ ] A `sensor_id` and `source_uri` both together make a unique stream, not just the sensor_id. Fix the stream attached error coming up for duplicate streams with the same `sensor_id` but different `source_uri`.
- [ ] Update the unique identifier (`stream_id`) for a stream to be a short uuid prefixed with `sensor_id`.

# Deferred

Some of these items might have been already reconsidered for the TODO list above.

- [ ] Defer pagination for `GET /streams`; it currently returns the full stream list, while pagination is implemented for `/records`
- [ ] Do not implement sidecar indexes for keyframes or decoding assistance
- [ ] Defer S3-compatible storage for archived media and live recording writes, including publishing, finalization, recovery, and cleanup
- [ ] Defer authentication and rate limiting; they are out of scope for now
