<!--
SPDX-FileCopyrightText: (C) 2026 Intel Corporation
SPDX-License-Identifier: Apache-2.0
-->

# Stream Manager API draft

`openapi.yaml` describes all 16 operations in `Stream_Manager_API.pdf`, plus
health and version resources (18 operations total): stream attachment/buffering/
stats, recording lifecycle and import, binary or signed-URL retrieval, and
operational information. It is an OpenAPI 3.0.3 design draft, not an implemented API.

## Health and version resources

These additions follow standard HTTP semantics and the existing `/v1` API
namespace. REST does not mandate specific health/version paths or JSON fields;
these are explicitly defined service conventions, not a separate REST standard.

| Endpoint | Success | Unavailable / failure |
|---|---|---|
| `GET /v1/health` | `200 application/json` with `service`, `status: ok`, and UTC `checked_at`. | `503 application/problem+json` using RFC 9457, with optional `Retry-After` seconds when estimable. |
| `GET /v1/version` | `200 application/json` with `service`, SemVer `version`, and `api_version`; optional `build_time` and `git_commit`. | Errors use the existing RFC 9457 problem-details contract. |

Both GET operations are read-only and idempotent. Successful responses and
health `503` responses require `Cache-Control: no-store` so probes do not receive
stale state. The version resource reports the running application release,
separately from the API route version and the OpenAPI document revision.

Health means **readiness**: initialization is complete and essential configured
dependencies, including active storage, are usable. Checks must be time-bounded.
Zero attached cameras is healthy; individual camera failures belong to stream
status/statistics. Do not use this dependency-aware endpoint for a liveness probe
that restarts the process. Version reads local build metadata and does not depend
on camera or storage availability. Deployment access policies still apply; do
not return sensitive infrastructure or configuration details in either response.

## Host Swagger UI with Go

Use the standalone [`swgui`](https://github.com/swaggest/swgui) Go tool.
No Docker, Compose, or custom server code is needed.

Install once (requires Go):

```bash
go install github.com/swaggest/swgui/cmd/swgui@v1.8.9
```

From the repository root, serve the spec:

```bash
~/go/bin/swgui -s -listen 0.0.0.0:43210 docs/stream-manager/openapi.yaml
```

This assumes Go's default installation directory (`~/go/bin`); adjust if you
customize `GOBIN` or `GOPATH`. On this machine Go is at `/usr/local/go/bin/go`;
add `/usr/local/go/bin` to `PATH` if `go` is not found.

Open **http://localhost:43210** locally, or `http://<host>:43210` remotely.
The YAML is also served at **http://localhost:43210/openapi.yaml**.
Swagger UI assets are embedded locally in
the Go binary; no CDN or external specification validator is used. `-s` skips
opening a browser on the server. Refresh the browser after editing the YAML.

This command stays in the foreground. On the current machine, the tool is
already installed and managed by the enabled **stream-manager-docs.service**
user service, with automatic restart and user lingering enabled. It survives
terminal closure/logout and starts again after reboot, provided the home
directory and this repository are available. Do not start a second foreground
instance on the same port while the service is running.

```bash
systemctl --user status stream-manager-docs --no-pager
systemctl --user restart stream-manager-docs
systemctl --user stop stream-manager-docs
```

The machine-specific unit is at
`~/.config/systemd/user/stream-manager-docs.service`; update its `ExecStart` if
you move the spec or binary. This is single-host hosting, not high availability;
host outages and explicitly stopped services cannot serve documentation.

The server binds to all IPv4 interfaces on port **43210** and serves
documentation, not the Stream Manager implementation. The viewer uses plain
HTTP without authentication: any client with network access to this port can
read the specification. Restrict access with firewall rules or an authenticated
TLS reverse proxy if needed; this setup does not change host firewall rules.
The API server URL `http://localhost:8081` in the
contract is a placeholder. Swagger UI's request controls cannot exercise these
APIs until an actual backend is implemented and configured. Do not enable the
tool's `-proxy` option or expose the unauthenticated viewer publicly.

If direct network access is unavailable, forward port **43210** using VS Code's
**Ports** panel, or run this on your laptop (replace `<host>` with the server's
SSH hostname):

```bash
ssh -N -L 43210:127.0.0.1:43210 intel@<host>
```

Then open the same localhost URL on your laptop.

## Contract decisions and open questions

The PDF is a requirements reference, not a complete wire contract. This draft
makes the following choices explicit rather than presenting them as settled
requirements:

| Topic | Draft behavior / remaining decision |
|---|---|
| Response shapes and states | Defines resource objects, `items`/`next_cursor` pages, record states, and HTTP statuses. Only the active-recording detach `409` is explicit in the PDF. |
| Recording times | `timestamp_start` is a required event anchor. Coverage starts before it by `pre_event_seconds`; fixed duration runs after it. Five seconds plus three seconds pre-roll means eight seconds total. Confirm whether the intended duration should instead include pre-roll. |
| Missing history | Reject unavailable pre-roll with `409`, atomically across selected tracks. No silent truncation. |
| Stop and readiness | Stop accepts asynchronous finalization. Retrieval requires `ready`; growing-record retrieval is not defined. |
| Exact frame identity | The PDF says both "nearest available" and no silent approximation. This draft returns the nearest frame with the actual timestamp and requires strict clients to reject mismatches. Whether the server should instead expose an exact-match mode remains open. |
| Correlation | Maps fields to `X-Capture-Timestamp`, `X-Keyframe-Distance-Ms`, and `X-Sync-Confidence`; retains the specified `X-Frame-Timestamp`. Clip headers and per-track correlation describe the first frame. URL responses include correlation in JSON. Exact capture timestamp equality across independent ingestion pipelines still needs a proven source-timestamp mapping. |
| Metadata filters | Uses `metadata[key]=value` (`deepObject`) to represent the PDF's conceptual `metadata.*` filters. This is a proposed encoding, not a literal dotted-key implementation. Filters use flat string equality. |
| Import | Adds required `sensor_id` and `timestamp_start` multipart fields to establish durable media identity; imported clock confidence is `unverified`. |
| Identifiers and limits | ID character rules, pagination limits, field sizes, format defaults, tie-breaking, and stats measurement window are draft choices. Media upload, extraction, concurrency, and storage quotas still need deployment-specific limits. |
| Idempotency | Same key and request must not create duplicate records; conflicting payloads return `409`. Key retention and simultaneous retries need final implementation semantics. |
| Authentication | Unspecified by the PDF. No fabricated auth protocol is imposed here. Production requires authorization, client scoping, credential management, and source destination controls. |
| Signed URLs | Server sets expiry and signs a proxy URL served from local filesystem storage. Do not persist/log signed URLs or grant access to unrelated footage in a larger segment. |
| Protocol details | RTSP/WebRTC source resolution, credential provisioning, reconnect policy, codec support, and clock-guarantee detection remain to be designed. |

Retrieval query XOR rules (one camera selector; one clip end/duration) and time
ordering are documented but require server-side validation. OpenAPI 3.0 cannot
express dependencies across separate query parameters. Request-body selector and
recording-mode constraints are expressed using `oneOf`/`not`.

The RAG linkage remains application-owned: persist `record_id`, `sensor_id` (or
`stream_id`), and the verified `capture_ts` alongside the caption and embedding.
Stream Manager does not generate or search embeddings.
