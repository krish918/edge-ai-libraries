# Get Started

This guide shows how to attach an RTSP source, record video, and retrieve a frame and clip.

## Requirements

- Go 1.26 or later.
- FFmpeg and FFprobe on `PATH`.
- `curl` and `jq` for the example commands.
- A reachable RTSP source with one H.264 or H.265 video track.
- A private temporary filesystem for the rolling buffer, normally `/dev/shm` on Linux.

The RTSP client uses TCP. Audio is ignored. The source URL must not contain a username or password.

## Configuration reference

The API server reads these settings from its environment. The root `.env.example` contains only
Docker Compose overrides; it is not a complete application configuration file.

### Service and metadata

| Variable | Default or requirement | Purpose |
|---|---|---|
| `STREAM_MANAGER_PORT` | `18080` | HTTP listen port. |
| `STREAM_MANAGER_VERSION` | `0.1.0` | Value returned by `GET /v1/version`. |
| `STREAM_MANAGER_SQLITE_PATH` | `/var/lib/stream-manager/stream-manager.db` | Absolute path to SQLite metadata. |
| `STREAM_MANAGER_STORAGE_BACKEND` | `filesystem` | Only `filesystem` is supported. |

### Filesystem media

These settings apply when `STREAM_MANAGER_STORAGE_BACKEND=filesystem`:

| Variable | Default or requirement | Purpose |
|---|---|---|
| `STREAM_MANAGER_FS_ROOT` | Required | Absolute root for recording and derived media. Use persistent storage. |
| `STREAM_MANAGER_PUBLIC_BASE_URL` | Required | Public service base URL used to create media links. |
| `STREAM_MANAGER_MEDIA_TOKEN_SECRET` | Required | Secret used to sign filesystem `/v1/media/{token}` capability links. |
| `STREAM_MANAGER_FS_MAX_STAGE_BYTES` | `2147483648` (2 GiB) | Maximum source size staged for extraction. Must be positive. |

### Streams and recordings

| Variable | Default or requirement | Purpose |
|---|---|---|
| `STREAM_MANAGER_BUFFER_DIR` | `/dev/shm/stream-manager` | Private tmpfs directory for rolling stream buffers; must be owned by the service account with mode `0700`. |
| `STREAM_MANAGER_BUFFER_LENGTH` | `30s`; range `5s` to `5m` | Default rolling-buffer window. Use a Go duration such as `45s` or `2m`. |
| `STREAM_MANAGER_MAX_ACTIVE_RECORDS` | `32` | Maximum concurrent recording workers; must be positive. |
| `STREAM_MANAGER_DEV_BEST_EFFORT_TIMESTAMPS` | `false` | Development-only fallback when a source has no usable RTCP/NTP mapping. |

### Replay and logging

| Variable | Default or requirement | Purpose |
|---|---|---|
| `STREAM_MANAGER_DERIVED_TTL` | `15m` | Retention hint for derived frames and clips. |
| `STREAM_MANAGER_PRESIGN_EXPIRY` | `5m` | Lifetime of generated media URLs. |
| `STREAM_MANAGER_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `STREAM_MANAGER_LOG_FORMAT` | `json` | `json` or `text`. |

## Configure and start

Use absolute paths on persistent
storage for the database and media. The service account must own these directories and have
read/write access. The rolling buffer stays on private tmpfs and is not persistent.

For a local persistent run, create a data directory owned by your user:

```bash
export DATA_DIR="$HOME/.local/state/stream-manager"
mkdir -p "$DATA_DIR/media"

export STREAM_MANAGER_STORAGE_BACKEND=filesystem
export STREAM_MANAGER_FS_ROOT="$DATA_DIR/media"
export STREAM_MANAGER_PUBLIC_BASE_URL=http://localhost:18080
export STREAM_MANAGER_MEDIA_TOKEN_SECRET='replace-with-a-random-secret'
export STREAM_MANAGER_SQLITE_PATH="$DATA_DIR/stream-manager.db"
export STREAM_MANAGER_BUFFER_DIR=/dev/shm/stream-manager
export STREAM_MANAGER_BUFFER_LENGTH=30s
export STREAM_MANAGER_MAX_ACTIVE_RECORDS=8
```

For a service deployment, provision `/var/lib/stream-manager` on persistent storage and make it
owned by the service account before startup. The service restricts the filesystem media root to
mode `0700`; the SQLite database and both lock files use mode `0600`. Run only one service process per
SQLite database path or filesystem media root because process locks reject concurrent openers. Set `STREAM_MANAGER_FS_ROOT` to
`/var/lib/stream-manager/media` and `STREAM_MANAGER_SQLITE_PATH` to
`/var/lib/stream-manager/stream-manager.db`. The service rejects relative database and filesystem
media paths. It cannot determine whether an absolute path is persistent; the deployment must mount
persistent storage at these locations.

By default, the service requires camera RTCP/NTP timing. For development only, enable a best-effort
clock if the camera does not provide a usable mapping:

```bash
export STREAM_MANAGER_DEV_BEST_EFFORT_TIMESTAMPS=true
```

Best-effort timestamps use the service host clock. They do not confirm the camera's capture time.
Do not enable this option in production.

Start the API server:

```bash
go run ./cmd/api-server
```

The default address is `http://localhost:18080`. In another terminal, set the base URL and source:

```bash
export BASE=http://localhost:18080
export RTSP_URL='rtsp://camera-host:554/path'
```

## Attach a stream

```bash
STREAM_JSON=$(curl -fsS -H 'Content-Type: application/json' \
	-d "{\"sensor_id\":\"camera-01\",\"source_kind\":\"uri_source\",\"source_uri\":\"$RTSP_URL\"}" \
	"$BASE/v1/streams")
STREAM_ID=$(printf '%s\n' "$STREAM_JSON" | jq -r '.stream_id')
printf '%s\n' "$STREAM_JSON" | jq
```

Wait for the stream to reach `buffering`. Check `sync_confidence` before recording. It is
`ntp_synced` when the source provides a usable clock mapping, or `best_effort` when the development
fallback is enabled.

```bash
while :; do
	STREAM_INFO=$(curl -fsS "$BASE/v1/streams/$STREAM_ID")
	STATE=$(printf '%s' "$STREAM_INFO" | jq -r '.state')
	[[ "$STATE" == buffering ]] && break
	if [[ "$STATE" == failed ]]; then
		printf '%s\n' "$STREAM_INFO" | jq
		exit 1
	fi
	sleep 1
done
printf '%s\n' "$STREAM_INFO" | jq
```

## Record video

Start a fixed-duration recording. `start_ts` must not be in the future. The stream must have buffer
coverage for the requested start time.

```bash
START_TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
RECORD_JSON=$(curl -fsS -H 'Content-Type: application/json' \
	-d "{\"stream_id\":\"$STREAM_ID\",\"start_ts\":\"$START_TS\",\"duration\":10,\"pre_event_duration\":0}" \
	"$BASE/v1/records/start")
RECORDING_ID=$(printf '%s' "$RECORD_JSON" | jq -r '.recordings[0].recording_id')
printf '%s\n' "$RECORD_JSON" | jq
```

Poll `GET /v1/records/{recording_id}` until the state is `ready` or `failed`:

```bash
while :; do
	RECORD=$(curl -fsS "$BASE/v1/records/$RECORDING_ID")
	STATE=$(printf '%s' "$RECORD" | jq -r '.state')
	[[ "$STATE" == ready ]] && break
	if [[ "$STATE" == failed ]]; then
		printf '%s\n' "$RECORD" | jq
		exit 1
	fi
	sleep 1
done
printf '%s\n' "$RECORD" | jq
```

The live recording files are stored under
`$STREAM_MANAGER_FS_ROOT/recordings/$RECORDING_ID/`. The media file is `media.ts`; its timestamp
index is `sidecar.jsonl`. The index contains one sample per complete keyframe-started slice, so
frame lookup uses keyframe-level timing. Extracted frames and clips are stored under
`$STREAM_MANAGER_FS_ROOT/derived/$RECORDING_ID/` and can be regenerated from the source recording.

## Retrieve a frame and clip

Read indexed timestamps from the sidecar. A clip needs a later index sample after its end time.

```bash
SIDECAR="$STREAM_MANAGER_FS_ROOT/recordings/$RECORDING_ID/sidecar.jsonl"
jq -c 'select(has("ordinal")) | {ordinal, capture_ts, pts}' "$SIDECAR"
mapfile -t SAMPLES < <(jq -r 'select(has("ordinal")) | .capture_ts' "$SIDECAR")
(( ${#SAMPLES[@]} >= 3 )) || { echo 'need at least three indexed samples'; exit 1; }
END_INDEX=$((${#SAMPLES[@]} - 2))

curl -fsS -H 'Accept: image/jpeg' --get \
	--data-urlencode "timestamp=${SAMPLES[0]}" --data-urlencode 'match=exact' \
	-o frame.jpg "$BASE/v1/replays/$RECORDING_ID/frame"
curl -fsS -H 'Accept: video/mp4' --get \
	--data-urlencode "start_ts=${SAMPLES[0]}" \
	--data-urlencode "end_ts=${SAMPLES[$END_INDEX]}" \
	-o clip.mp4 "$BASE/v1/replays/$RECORDING_ID/clip"
file frame.jpg clip.mp4
```

The `/frame/url` and `/clip/url` routes return JSON with a short-lived URL. See the
[API reference](api-reference.md) for details and cleanup commands.
