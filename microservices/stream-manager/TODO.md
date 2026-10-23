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
- [x] Disable expiry of retrieved clips and frames by default with `DerivedMediaTTL: -1`; retain per-recording expiry and allow positive TTL values to be enabled later
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
- [x] Evaluate whether `DecodeJSON` would be useful as middleware — evaluated, not needed. Each endpoint decodes a different request type, so middleware would need generics or context passing for four short call sites. Keep the shared helper in `json_request.go`.
- [ ] Optimize request sequences - Should POST /streams be always called before POST /records/start by consumer?
- [x] Make the derived media TTL configurable via `config.json`; `-1` disables TTL expiry by default
- [x] Add a cleanup job that deletes expired recordings with all their derived media. A recording's `expiry_ts` always wins: when the recording expires, its derived items are deleted with it even if their own TTL has not run out
- [ ] Extend the cleanup job to delete derived media in `<recording-dir>/derived/` whose own TTL has run out, while the recording itself is still kept
- [ ] Decide whether `PUT /streams/{stream-id}/buffer` should reject resizing a stream that is in `failed` state, or whether resizing an already-failed stream's buffer should keep succeeding as it does today
- [ ] Should recording IDs be prefixed with `rec-` as stream IDs are prefixed with `str-`?
- [ ] Skip the startup scan of recording folders after a clean shutdown. On clean stop, save the quota usage to `.usage.json` with a clean-shutdown mark. On start, load it when the mark exists and then remove the mark; otherwise (crash, `kill -9`, forced container stop) do the full scan as today
- [x] Delete leftover `derived/.tmp-*` files at startup; a crash during frame or clip extraction leaves them on disk and in the quota
- [x] Remove the SQLite index on `expiry_ts`; keep the expiry cleaner's full scan for the current workload
- [x] Run expired-recording cleanup at startup and every 24 hours
- [ ] Let the expiry cleaner delete media outside the record service lock (mark the recording as deleting under the lock, delete files after), so slow disks do not delay start/stop/delete requests
- [x] Delete recording metadata first, then attempt media deletion; retry leftover media without metadata during cleanup at startup and every 24 hours

## Research/Exploration
- [x] Weigh in tradeoffs of using random UUIDs as primary key in the database - should UUIDv7 be used or a shorter UUID or incremental int prefixed to UUIDs.
- [x] Analyse tradeoffs of using distroless/scratch base image for the container - whether its even feasible or practical for our use case.
- [ ] Should the retrieval (replay APIs) of clip and frame be supported while the recording is ongoing - and not `finalized` or `ready` yet.
- [ ] Evaluate S3-compatible storage tradeoffs for video recordings, especially open recordings and real-time ingestion
- [ ] Evaluate whether fMP4 or another format can reduce MPEG-TS storage overhead while retaining its robustness
- [ ] Evaluate WebM instead of MP4 as the default Replay API clip format to use fully open-source codecs

## Bugs/Issues

- [x] A `sensor_id` and `source_uri` both together make a unique stream, not just the sensor_id. Fix the stream attached error coming up for duplicate streams with the same `sensor_id` but different `source_uri`.
- [x] Even if buffering or stream attachment fails, a re-attachment request fails with Stream_exists. This should not be the behavior.
- [x] Update the unique identifier (`stream_id`) for a stream to be a short uuid prefixed with `sensor_id`. (See related item in Research/Exploration)
- [x] Update OpenAPI specs error response and error codes to be inline with actual implementation.
- [x] Fix incorrect examples for /clip and /frame endpoints in OpenAPI specs.
- [x] Handle orphan files after `DELETE /records/{recording-id}`. Delete metadata first, attempt media deletion, and let cleanup retry leftover media. Metadata deletion failure returns 503 `storage_unavailable`; deferred media deletion returns 200.
- [x] Frame and clip extraction near the end of a long recording read the whole recording first. FFprobe now reads only time windows around the requested time, and FFmpeg seeks to the keyframe before it.
- [x] Frame and clip extraction falls back to reading from the recording start when no frame is found within ±10 s or no keyframe within 1 min (long gaps or sparse keyframes). Decide whether to cap this or fail fast. — Capped: the search stops at 5 minutes (`retrieve.SearchLimit`) and returns 404 `frame_not_found` / `clip_not_found`. Keep the search limit in internal logs, not API responses.
- [ ] MPEG-TS (and also other alternative fMP4) are unseekable. Cut clips are seekable (MP4). Analyze whether this is a prospective issue.
- [ ] Debug - (intermittent) "no space left on device" issue in docker container while buffering.
- [x] API Best Practices: consolidate /clip and /clip/url (similarly /frame and /frame/url) into retrieval endpoints with Accept negotiation and media URLs.
- [x] Remove the confusing `content_type` field from frame and clip JSON responses; retain HTTP Content-Type headers for binary responses.

# Deferred

Some of these items might have been already reconsidered for the TODO list above.

- Defer pagination for `GET /streams`; pagination is currently planned only for `/records` endpoints
- Do not implement sidecar indexes for keyframes or decoding assistance
- Defer S3-based storage
- Defer authentication and rate limiting; they are out of scope for now
