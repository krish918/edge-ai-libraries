<!-- SPDX-FileCopyrightText: (C) 2026 Intel Corporation -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# TODO

Items are listed roughly in order of priority.

## Features

### Major
- [x] Design and implement DATA dir setup and handling logic for both host and containerised environments
- [x] Package and distribution - Discuss and implement a strategy for containerizing.
- [x] Implement the `PUT /buffer` endpoint to resize stream buffers
- [ ] Add S3-compatible storage for videos
- [ ] Discuss, analyse and implement multiple `stream_ids` in start-recording request bodies
- [ ] Discuss if TTL or expiry of retrieved clips and frames is a desired or undesired feature. Act accordingly.
- [x] Documentation Update - With forked repo references
- [ ] Documentation Update - With mainline repo references

### Incremental
- [x] Implement support for user supplied config file. JSON or YAML preferred.
- [x] Add a tool for building and running the application on host and as a container (`Makefile` with `build`/`run`/`start`/`docker-build`/`docker-run`/`docker-stop` targets).
- [ ] Add pagination to the `GET /streams` endpoint
- [ ] Decide whether `make deploy` should add stream-manager (with its config and data mounts) to an existing docker compose file, which is passed as an argument.
- [ ] Analyse if ranger headers needed in media access APIs and implement accordingly

## Optimization or Improvements

### Major
- [x] Add a custom error-handling framework using `StreamManError`
- [ ] See possibilities to use `StreamManError` across all error chains, or even as Custom Sentinel Errors.
- [x] Write `AGENTS.md` and `copilot-instructions.md`
- [ ] Idempotency of the `POST /records/start` endpoint

### Incremental
- [x] Use of `log/slog` for logging
- [ ] Check if `ffmpeg` can completely replace `blueviron/mediamtx` and whether it will be an optimal choice. (or vice-versa :O)
- [x] Check `cfg.RecordingStorage` quotas and enforce disk limits through host-based notifications or other mechanisms (rolling storage?)
- [x] Use `errors.Join()` to combine `recordingFilter` validation errors
- [ ] Use `errors.Join()` to combine similar validation errors in other endpoints (stream, record start/stop, replay)
- [x] Evaluate whether `DecodeJSON` would be useful as middleware — evaluated, not needed. Each endpoint decodes a different request type, so middleware would need generics or context passing for four short call sites. Moved `DecodeJSON` from `middleware.go` to `decode.go`.
- [ ] Optimize request sequences - Should POST /streams be always called before POST /records/start by consumer?
- [x] Make the derived media TTL (fixed 15 min for replay frames and clips) configurable via `config.json`
- [x] Add a cleanup job that deletes expired recordings with all their derived media. A recording's `expiry_ts` always wins: when the recording expires, its derived items are deleted with it even if their own TTL has not run out
- [ ] Extend the cleanup job to delete derived media in `<recording-dir>/derived/` whose own TTL has run out, while the recording itself is still kept
- [ ] Decide whether `PUT /streams/{stream-id}/buffer` should reject resizing a stream that is in `failed` state, or whether resizing an already-failed stream's buffer should keep succeeding as it does today
- [ ] Skip the startup scan of recording folders after a clean shutdown. On clean stop, save the quota usage to `.usage.json` with a clean-shutdown mark. On start, load it when the mark exists and then remove the mark; otherwise (crash, `kill -9`, forced container stop) do the full scan as today
- [x] Delete leftover `derived/.tmp-*` files at startup; a crash during frame or clip extraction leaves them on disk and in the quota
- [x] Add a SQLite index on `expiry_ts` so the expiry cleaner does not scan every recording row each minute
- [ ] Let the expiry cleaner delete media outside the record service lock (mark the recording as deleting under the lock, delete files after), so slow disks do not delay start/stop/delete requests
- [ ] Decide whether a `DELETE /records/{recording-id}` that fails on storage should be retried in the background (for example, mark the recording as `deleting` and let the cleaner finish it), instead of the client having to retry

## Research/Exploration
- [x] Weigh in tradeoffs of using random UUIDs as primary key in the database - should UUIDv7 be used or a shorter UUID or incremental int prefixed to UUIDs.
- [x] Analyse tradeoffs of using distroless/scratch base image for the container - whether its even feasible or practical for our use case.
- [ ] Evaluate S3-compatible storage tradeoffs for video recordings, especially open recordings and real-time ingestion
- [ ] Evaluate whether fMP4 or another format can reduce MPEG-TS storage overhead while retaining its robustness
- [ ] Evaluate WebM instead of MP4 as the default Replay API clip format to use fully open-source codecs

## Bugs/Issues

- [x] A `sensor_id` and `source_uri` both together make a unique stream, not just the sensor_id. Fix the stream attached error coming up for duplicate streams with the same `sensor_id` but different `source_uri`.
- [x] Even if buffering or stream attachment fails, a re-attachment request fails with Stream_exists. This should not be the behavior.
- [x] Update the unique identifier (`stream_id`) for a stream to be a short uuid prefixed with `sensor_id`. (See related item in Research/Exploration)
- [x] Update OpenAPI specs error response and error codes to be inline with actual implementation.
- [x] Fix incorrect examples for /clip and /frame endpoints in OpenAPI specs.
- [x] `DELETE /records/{recording-id}` deleted the database row before the media, returned 200 even when media deletion failed, and left orphan files. Media is now deleted first, a failure returns 503 `storage_unavailable` (or 409 `active_dependency` when media is in use), and the request is safe to retry.
- [x] Frame and clip extraction near the end of a long recording read the whole recording first. FFprobe now reads only time windows around the requested time, and FFmpeg seeks to the keyframe before it.
- [x] Frame and clip extraction falls back to reading from the recording start when no frame is found within ±10 s or no keyframe within 1 min (long gaps or sparse keyframes). Decide whether to cap this or fail fast. — Capped: the search stops at 5 minutes (`replay.SearchLimit`) and returns 404 `frame_not_found` / `clip_not_found`.
- [ ] MPEG-TS (and also other alternative fMP4) are unseekable. Cut clips are seekable (MP4). Analyze whether this is a prospective issue.
- [ ] Debug - (intermittent) not disk space left issue in docker container while buffering.

# Deferred

Some of these items might have been already reconsidered for the TODO list above.

- Defer pagination for `GET /streams`; pagination is currently planned only for `/records` endpoints
- Do not implement sidecar indexes for keyframes or decoding assistance
- Defer S3-based storage
- Defer authentication and rate limiting; they are out of scope for now
