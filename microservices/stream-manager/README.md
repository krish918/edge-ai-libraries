# Stream Manager

Stream Manager attaches to RTSP video sources, keeps a rolling buffer, records selected intervals,
and serves frames and clips by timestamp.

Live stream, recording, and replay operations use filesystem storage. Recording metadata is stored
in SQLite. FFmpeg handles recording and frame/clip extraction.

For filesystem deployments, configure absolute paths on persistent storage. The recommended layout
uses `/var/lib/stream-manager/media` for recordings and derived clips/frames, and
`/var/lib/stream-manager/stream-manager.db` for SQLite. The rolling buffer belongs on private tmpfs,
not on persistent storage. See [Get Started](docs/user-guide/get-started.md) for directory setup.

## Implemented features

- Attach one H.264 or H.265 RTSP video track over TCP and inspect its buffer and timestamp confidence.
- Record a fixed interval or start and stop a recording manually.
- Retrieve a JPEG/PNG frame or an MP4 clip, as media bytes or a JSON response with a media URL.
- Store live media as MPEG-TS with a JSONL timestamp index.

## Run with an RTSP source

See [Get Started](docs/user-guide/get-started.md) for prerequisites and a complete recording and
replay example. See the [API reference](docs/user-guide/api-reference.md) for routes, request fields,
responses, and errors.

## Build

```bash
go mod download
CGO_ENABLED=0 go build ./...
```

The SQLite driver is pure Go. FFmpeg and FFprobe must be available on `PATH` to run live recording
and media extraction.

## Run in Docker

Docker Compose builds the API image and starts the filesystem-backed service:

```bash
cp .env.example .env
docker compose up --build -d
curl -fsS http://localhost:18080/v1/health
docker compose down
```

The named volume keeps the database and media between runs. The rolling buffer uses container
tmpfs and is cleared when the container stops. The example media-token secret is for local
development only; replace it with a unique secret before starting a shared deployment. Edit
`.env` to change the host port or other Compose overrides. If you change the host port, update
`STREAM_MANAGER_PUBLIC_BASE_URL` to use the same port.
