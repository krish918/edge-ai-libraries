# Interfaces Specs for Stream Manager

| Area | Interface | Responsibility |
|------|-----------|----------------|
| Storage | `MediaHandler` | Save, retrieve, and delete media files |
| Storage | `MetadataHandler` | Save, retrieve, list, update, and delete metadata |
| Stream | `Bufferer` | Create buffers, get video slices, and remove buffers |
| Recording | `Recorder` | Start, stop, and manage recordings |
| Replay | `Replayer` | Extract clips and frames from recorded media |

## Storage Interfaces

### 1. MediaHandler

Responsible for handling media storage operations such as saving, retrieving, and deleting media files.

| Method | Parameters | Returns |
|--------|------------|---------|
| `PutMedia` | `ctx context.Context`<br>`recordingPath string` | `(string, error)` |
| `OpenMedia` | `ctx context.Context`<br>`recordingPath string` | `(string, error)` |
| `DeleteMedia` | `ctx context.Context`<br>`recordingPath string` | `error` |

### 2. MetadataHandler

Responsible for handling metadata storage operations such as saving, retrieving, and deleting metadata.

| Method | Parameters | Returns |
|--------|------------|---------|
| `CreateMetadata` | `ctx context.Context`<br>`metadata Recording` | `(Recording, error)` |
| `GetMetadataByRecordingID` | `ctx context.Context`<br>`recording_id string` | `(Recording, error)` |
| `ListMetadata` | `ctx context.Context`<br>`recordingFilter RecordingFilter` | `([]Recording, string, error)` |
| `UpdateMetadata` | `ctx context.Context`<br>`recording_id string`<br>`metadata Recording` | `(Recording, error)` |
| `DeleteMetadata` | `ctx context.Context`<br>`recording_id string` | `error` |

## Stream Interface

### 1. Bufferer

Responsible for handling stream buffering operations such as creating buffers, adding video slices, getting video slices, and removing buffers.

| Method | Parameters | Returns |
|--------|------------|---------|
| `CreateBuffer` | `ctx context.Context`<br>`source_uri string`<br>`sensor_id string` | `(string, error)` |
| `GetBuffer` | `ctx context.Context`<br>`stream_id string`<br>`start_ts time.Time`<br>`end_ts time.Time` | `([]BufferSlice, error)` |
| `RemoveBuffer` | `ctx context.Context`<br>`stream_id string` | `error` |
| `GetStream` | `ctx context.Context`<br>`stream_id string` | `(StreamBuffer, error)` |
| `ListStreams` | `ctx context.Context` | `([]StreamBuffer, error)` |

## Recording Interface

### 1. Recorder

Responsible for handling recording operations such as starting, stopping, and managing recordings.

| Method | Parameters | Returns |
|--------|------------|---------|
| `StartRecording` | `ctx context.Context`<br>`sensor_id string`<br>`stream_id string`<br>`start_ts time.Time`<br>`end_ts time.Time`<br>`pre_event_duration int`<br>`metadata map[string]interface{}` | `(Recording, error)` |
| `StopRecording` | `ctx context.Context`<br>`recording_id string` | `error` |
| `DeleteRecording` | `ctx context.Context`<br>`recording_id string` | `error` |

## Replay Interface

### 1. Replayer

Responsible for handling clip and frame extraction from the recorded media.

| Method | Parameters | Returns |
|--------|------------|---------|
| `ExtractClip` | `ctx context.Context`<br>`recording_id string`<br>`start_ts time.Time`<br>`clip_duration int`<br>`format string` | `([]byte, error)` |
| `GetClipURL` | `ctx context.Context`<br>`recording_id string`<br>`start_ts time.Time`<br>`clip_duration int`<br>`format string` | `(MediaResult, error)` |
| `ExtractFrame` | `ctx context.Context`<br>`recording_id string`<br>`timestamp time.Time`<br>`format string`<br>`match string` | `([]byte, error)` |
| `GetFrameURL` | `ctx context.Context`<br>`recording_id string`<br>`timestamp time.Time`<br>`format string`<br>`match string` | `(MediaResult, error)` |
