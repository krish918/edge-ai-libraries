<!-- SPDX-FileCopyrightText: (C) 2026 Intel Corporation -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Configuration

Stream Manager works out of the box with built-in default settings. You do not need
a `config.json` file to run it.

To change a setting, create a `config.json` file. You only need to list the settings
you want to change. Stream Manager uses its default value for any setting you leave out.

You can also start from the sample `config.json` file in the repository root. Copy it,
then edit only the values you need.

This page lists each setting, its purpose, and its default value.

## config.json fields

| Field | Type | Unit | Default | Purpose |
| --- | --- | --- | --- | --- |
| `HTTPAddr` | string | — | `:8080` | Address and port the HTTP server listens on. |
| `BufferLength` | integer | seconds | `30` | Default rolling buffer length for a new stream. A request to `POST /streams` can override this per stream. Must be between 5 and 300. |
| `StorageDir` | string | — | `~/.local/share/stream-manager` | Directory where the database and recordings are stored. |
| `ConcurrentRecorders` | integer | count | `10` | Maximum number of recordings that can run at the same time. |
| `RecordingStorageSize` | integer | megabytes | `10240` | Storage quota for recorded media and the frames and clips cut from it. Must be between 5120 and 1073741824. |
| `RecordingRetention` | integer | hours | `0` | Time from completion until a recording and its derived frames and clips expire. Cleanup deletes all media for expired recordings. `0` does not set an expiry time. Must be between 0 and 360. |

### Storage quota

Stream Manager counts the bytes of each recording and of its frames and clips.

- When the quota is full, `POST /records/start` returns `507` with `storage_full`.
- When a running recording fills the quota, the recording stops early with the state
  `ready`. `end_ts` is the end of the last saved video. `error_detail` is
  `truncated: recording storage quota reached`.
- When a recording fills the quota before it saves video, the recording stops with the
  state `failed`. Stream Manager deletes its media.
- When the quota is full, a new frame or clip request returns `507` with `storage_full`.

Stream Manager does not delete unexpired recordings to make space. Delete recordings
manually to free space immediately. Retention and cleanup free space on the cleanup
schedule.

> [!NOTE]
> The quota does not include the database file. A recording can go above the quota by
> a small amount when FFmpeg finishes the file.

### Cleanup schedule

Cleanup runs at startup and every 24 hours while the service runs. No additional
grace period applies after recording expiry.

- Cleanup deletes expired recordings and their derived frames and clips.
- Cleanup deletes orphaned media, including frames and clips without recording metadata.
- Cleanup skips active recordings and recordings used by frame or clip extraction.
- Cleanup retries failed deletions on the next run.
- Cleanup frees the storage space used by deleted recordings, frames, and clips.

Recording expiry blocks new frame and clip extraction and downloads of existing
frames and clips immediately. Media deletion can occur up to 24 hours
later during normal operation. An active recording, extraction, or deletion failure
can delay cleanup until a later run.

Changing `RecordingRetention` does not change expiry times already assigned.
Cleanup still processes previously expired recordings when `RecordingRetention` is
`0`. Manual deletion removes a recording and its frames and clips without waiting
for the schedule. If extraction is active, manual deletion returns `409`; retry later.

### Frame and clip lifetime

Frames and clips use the recording's expiry time. They have no separate TTL.
Media responses report this time as `expiry_ts`. Downloads return `404` after expiry
or recording deletion. If the recording has no expiry, `expiry_ts` is `null`.
Frames and clips count toward the storage quota until cleanup or manual deletion
deletes their files.

## Example

```json
{
  "HTTPAddr": "0.0.0.0:8080",
  "BufferLength": 30,
  "StorageDir": "~/.local/share/stream-manager",
  "ConcurrentRecorders": 10,
  "RecordingStorageSize": 10240,
  "RecordingRetention": 0
}
```

## Where Stream Manager looks for config.json

By default, Stream Manager reads `config.json` from its current working directory.
Set the `SM_CONFIG_FILE` environment variable to use a file at a different path.

## Overrides inside a container

Two settings are fixed inside the published container image, and always win over
`config.json`:

| Setting | Fixed value | Set via |
| --- | --- | --- |
| `HTTPAddr` | `:8080` | `SM_HTTP_ADDR` environment variable |
| `StorageDir` | `/data` | `SM_DATA_DIR` environment variable |

Mount your own data volume or directory at `/data`, as shown in
[Get Started](get-started.md). Do not try to change the listen port or storage
path through a mounted `config.json` — it will have no effect.

See [Get Started](get-started.md) and [Build From Source](build-from-source.md) for how
to run Stream Manager with a custom `config.json`.
