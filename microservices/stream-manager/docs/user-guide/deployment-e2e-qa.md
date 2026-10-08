<!-- SPDX-FileCopyrightText: (C) 2026 Intel Corporation -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Deployment End-to-End QA

Use this checklist to validate a built Stream Manager image against a reachable RTSP source. The
examples use `rtsp://10.223.126.57:8554/fall`; replace it when that source is unavailable.

## Deploy the image

Run these commands from the `microservices/stream-manager` component directory. Confirm the source
is reachable from both the host and the container network. FFprobe should report one H.264 or H.265
video track. The RTSP client uses TCP.

```bash
RTSP_URL=rtsp://10.223.126.57:8554/fall
ffprobe -v error -rtsp_transport tcp -show_entries stream=codec_name,codec_type,width,height \
  -of json "$RTSP_URL"
docker compose build stream-manager
```

For a source without RTCP/NTP time mapping, enable the development-only fallback for this test
deployment:

```bash
STREAM_MANAGER_DEV_BEST_EFFORT_TIMESTAMPS=true docker compose up -d stream-manager
docker compose ps
curl -fsS http://localhost:18080/v1/health
curl -fsS http://localhost:18080/v1/version
```

Do not use `STREAM_MANAGER_DEV_BEST_EFFORT_TIMESTAMPS=true` as a production workaround. A test
that needs verified capture time must pass with the setting disabled and report
`sync_confidence: ntp_synced`. A source that only passes with this fallback is a development pass,
not a production timestamp-accuracy pass. Set `STREAM_MANAGER_PUBLIC_BASE_URL` to an address
reachable by the QA client before following returned media URLs from another host.

Follow [Get Started](get-started.md) to attach the RTSP source, record footage, and retrieve media.
The route matrix below lists the deployed endpoints that QA must cover.

## Route coverage

| Area | Routes | Acceptance checks |
|---|---|---|
| Service | `GET /v1/health`, `GET /v1/version` | Health is 200; version matches the deployed artifact. |
| API docs | `GET /docs/v1/index.html`, `GET /docs/v1/openapi.yaml` | UI and spec load from the deployed image. |
| Streams | `POST /v1/streams`, `GET /v1/streams`, `GET /v1/streams/{id}`, `PUT /v1/streams/{id}/buffer`, `DELETE /v1/streams/{id}` | Attach returns 201 and a `Location`; stream reaches `buffering`; list/get agree; valid buffer resize takes effect; deletion releases the source. |
| Recording lifecycle | `POST /v1/records/start`, `POST /v1/records/stop`, `GET /v1/records`, `GET /v1/records/{id}`, `DELETE /v1/records/{id}` | Start returns 201; stop returns 202 for a running manual recording; state reaches `ready`; filters and metadata are preserved; delete removes the recording and derived media. |
| Replay | `GET /v1/replays/{id}/frame`, `GET /v1/replays/{id}/frame/url`, `GET /v1/replays/{id}/clip`, `GET /v1/replays/{id}/clip/url` | Binary responses decode as JPEG/PNG or MP4; URL responses have correct type and timestamps; exact/nearest matching is accurate; clip duration and boundaries are correct. |
| Derived media | `GET /v1/media/{token}` | A valid URL fetches the expected media; expired, modified, deleted, or unknown media tokens fail with 404. |

## QA focus

- **Source and codec:** Test supported H.264 and H.265 cameras, video-only sources, and sources with audio. Confirm the service ignores audio, uses RTSP/TCP, and rejects unsupported or ambiguous video tracks and URLs containing credentials.
- **Time confidence:** Verify `ntp_synced` with a camera that provides usable RTCP/NTP mapping. Compare capture timestamps to the source clock and host clock. Separately verify the opt-in development fallback reports `best_effort`; never accept that value as proof of capture-time accuracy.
- **Connection lifecycle:** Check `connecting` to `buffering`, source disconnects, malformed/lost RTP, stream reattachment, duplicate sensor IDs, and cleanup after failed attachment. Confirm the API exposes failure rather than leaving a dead stream appearing healthy.
- **Rolling buffer:** Check oldest/newest timestamps, held bytes, buffer resize limits, expiry, and requests at or outside available history. Exercise recordings that start near the oldest retained slice and verify gaps or expired history fail clearly.
- **Recording lifecycle:** Cover fixed-duration and manual start/stop recordings; poll `recording`/`finalizing` to `ready` or `failed`. Verify concurrent-recording limits, active-record deletion rejection, metadata and list filters, and cleanup of media plus SQLite metadata.
- **Replay correctness:** Use timestamps from the recording's sidecar. Check JPEG and PNG frames, exact and nearest matching, MP4 duration/end boundaries, unsupported formats, invalid timestamps, and intervals crossing missing media. Decode outputs with FFprobe or an equivalent media validator.
- **Media links:** Fetch both frame and clip URLs from the QA client, verify expiry and content type, and confirm a URL stops working after expiry or object deletion. Treat capability URLs as short-lived bearer links and do not publish their token values in logs or test reports.
- **Resilience and capacity:** Exercise RTSP loss, filesystem errors, full tmpfs/disk, FFmpeg failures, concurrent streams/recordings, and service restart. Check that the service returns the documented error envelope and does not leak buffers, temporary files, or active workers.
- **Deployment configuration:** Confirm the container runs as its non-root service account, the media root is mode `0700`, SQLite and both lock files are mode `0600`, persistent media/SQLite paths survive restart, the rolling buffer uses private tmpfs, and the public base URL is reachable from clients. Run only one process per SQLite database path and filesystem media root. Replace the local development media-token secret for shared environments.

## Execution record

The supplied source was tested against a locally built `stream-manager:local` image. FFprobe read a
1920x1080 H.264 stream at approximately 30 fps from both the host and container. The service
reached `buffering` and recorded/replayed an 18-second sample after the development timestamp
fallback was enabled. Health, version, docs, stream, record, replay, media-token, and deletion routes
passed; JPEG and four-second MP4 outputs decoded successfully. The stream reported
`sync_confidence: best_effort`, so this run does not verify camera capture-time accuracy. Test
recording and stream resources were deleted after the run.