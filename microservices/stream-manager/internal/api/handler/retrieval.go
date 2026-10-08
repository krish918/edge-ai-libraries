// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/api/common"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/model"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/replay"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/storage"
)

// RetrievalHandler serves frame and clip retrieval endpoints.
type RetrievalHandler struct {
	service *replay.RetrievalService
	media   storage.MediaStore
}

func NewRetrievalHandler(service *replay.RetrievalService, media storage.MediaStore) *RetrievalHandler {
	return &RetrievalHandler{service: service, media: media}
}

// GetFrame serves a single frame, negotiating binary or JSON.
func (h *RetrievalHandler) GetFrame(c *gin.Context) {
	h.handleFrame(c, false)
}

// GetFrameURL always answers with a JSON MediaResult carrying a presigned
// URL, regardless of the Accept header.
func (h *RetrievalHandler) GetFrameURL(c *gin.Context) {
	h.handleFrame(c, true)
}

// GetClip serves a clip, negotiating binary or JSON.
func (h *RetrievalHandler) GetClip(c *gin.Context) {
	h.handleClip(c, false)
}

// GetClipURL always answers with a JSON MediaResult carrying a presigned
// URL, regardless of the Accept header.
func (h *RetrievalHandler) GetClipURL(c *gin.Context) {
	h.handleClip(c, true)
}

func (h *RetrievalHandler) handleFrame(c *gin.Context, forceJSON bool) {
	request, code, err := (frameQuery{
		RecordingID: c.Param("recording_id"),
		Timestamp:   c.Query("timestamp"),
		Format:      c.Query("format"),
		Match:       c.Query("match"),
	}).validate()
	if err != nil {
		common.WriteError(c, http.StatusBadRequest, code, err.Error())
		return
	}

	if !h.negotiate(c, forceJSON, request.Format, replay.FrameContentType, acceptsFrameBinary, "accept header is incompatible with the requested frame format") {
		return
	}

	result, err := h.service.GetFrame(c.Request.Context(), request)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}

	h.respond(c, result, forceJSON, acceptsFrameBinary)
}

func (h *RetrievalHandler) handleClip(c *gin.Context, forceJSON bool) {
	request, code, err := (clipQuery{
		RecordingID: c.Param("recording_id"),
		StartTS:     c.Query("start_ts"),
		EndTS:       c.Query("end_ts"),
		Duration:    c.Query("duration"),
		Format:      c.Query("format"),
	}).validate()
	if err != nil {
		common.WriteError(c, http.StatusBadRequest, code, err.Error())
		return
	}
	if !h.negotiate(c, forceJSON, request.Format, replay.ClipContentType, acceptsClipBinary, "accept header is incompatible with the requested clip format") {
		return
	}

	result, err := h.service.GetClip(c.Request.Context(), request)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}

	h.respond(c, result, forceJSON, acceptsClipBinary)
}

// negotiate checks the Accept header against the media type the request will
// produce before any storage or extraction work happens. It returns false
// after writing a 406 when the caller cannot accept either the binary media
// or the JSON descriptor. Unsupported formats are left for the service to
// report as 415 so the error precedence stays unchanged.
func (h *RetrievalHandler) negotiate(c *gin.Context, forceJSON bool, format string, contentTypeFor func(string) (string, error), acceptsBinary func(accept, contentType string) bool, incompatibleDetail string) bool {
	if forceJSON {
		return true
	}
	contentType, err := contentTypeFor(format)
	if err != nil {
		return true
	}
	accept := c.GetHeader("Accept")
	if acceptsBinary(accept, contentType) || acceptsJSON(accept) {
		return true
	}
	common.WriteError(c, http.StatusNotAcceptable, "not_acceptable", incompatibleDetail)
	return false
}

// respond applies the shared content-negotiation rules: the dedicated /url
// routes always return JSON, and the binary routes return bytes only when
// the caller explicitly asked for a compatible media type. negotiate has
// already rejected callers that accept neither representation.
func (h *RetrievalHandler) respond(c *gin.Context, result model.MediaResult, forceJSON bool, acceptsBinary func(accept, contentType string) bool) {
	if !forceJSON && acceptsBinary(c.GetHeader("Accept"), result.ContentType) {
		h.writeBinary(c, result)
		return
	}
	writeMediaResult(c, result)
}

// writeMediaResult writes a MediaResult (the JSON alternative to raw frame
// or clip bytes). Per the API contract this response is never cached, same
// as the binary alternative.
func writeMediaResult(c *gin.Context, result model.MediaResult) {
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, result)
}

func (h *RetrievalHandler) writeBinary(c *gin.Context, result model.MediaResult) {
	reader, err := h.media.OpenDerived(c.Request.Context(), result.DerivedKey)
	if err != nil {
		common.WriteError(c, http.StatusServiceUnavailable, "storage_unavailable", "failed to read derived media")
		return
	}
	defer reader.Close()

	extraHeaders := map[string]string{
		"Cache-Control":     "private, no-store",
		"X-Frame-Timestamp": result.StartTS.UTC().Format(time.RFC3339Nano),
		"X-Exact-Match":     strconv.FormatBool(result.ExactMatch),
	}
	// Content length is unknown up front (OpenDerived only hands back a
	// stream), so pass -1 and let Gin/net/http fall back to chunked
	// transfer encoding.
	c.DataFromReader(http.StatusOK, -1, result.ContentType, reader, extraHeaders)
}

func acceptsJSON(accept string) bool {
	accept = strings.ToLower(accept)
	return accept == "" || strings.Contains(accept, "application/json") || strings.Contains(accept, "*/*")
}

func acceptsFrameBinary(accept, contentType string) bool {
	accept = strings.ToLower(accept)
	if acceptsJSON(accept) {
		return false
	}
	if !strings.Contains(accept, "image/") {
		return false
	}
	switch contentType {
	case "image/jpeg":
		return strings.Contains(accept, "image/jpeg") || strings.Contains(accept, "image/*")
	case "image/png":
		return strings.Contains(accept, "image/png") || strings.Contains(accept, "image/*")
	default:
		return false
	}
}

func acceptsClipBinary(accept, contentType string) bool {
	accept = strings.ToLower(accept)
	if acceptsJSON(accept) {
		return false
	}
	if contentType != "video/mp4" {
		return false
	}
	return strings.Contains(accept, "video/mp4") ||
		strings.Contains(accept, "video/*") ||
		strings.Contains(accept, "application/octet-stream")
}

// writeServiceError maps retrieval failures to the API envelope. It returns
// generic details to clients and attaches the underlying error for access logs.
func (h *RetrievalHandler) writeServiceError(c *gin.Context, err error) {
	_ = c.Error(err)
	switch {
	case errors.Is(err, storage.ErrRecordingNotFound):
		common.WriteError(c, http.StatusNotFound, "recording_not_found", "recording not found")
	case errors.Is(err, storage.ErrInvalidIdentifier), errors.Is(err, storage.ErrInvalidObjectKey):
		common.WriteError(c, http.StatusBadRequest, "invalid_request", "invalid recording identifier")
	case errors.Is(err, replay.ErrRecordingNotReady):
		common.WriteError(c, http.StatusConflict, "recording_not_ready", "recording is not ready for retrieval")
	case errors.Is(err, replay.ErrUnsupportedFrameFormat):
		common.WriteError(c, http.StatusUnsupportedMediaType, "unsupported_media", "unsupported frame format")
	case errors.Is(err, replay.ErrUnsupportedClipFormat):
		common.WriteError(c, http.StatusUnsupportedMediaType, "unsupported_media", "unsupported clip format")
	case errors.Is(err, replay.ErrUnsupportedMatchMode):
		common.WriteError(c, http.StatusBadRequest, "invalid_request", "unsupported match mode")
	case errors.Is(err, replay.ErrInvalidClipRange):
		common.WriteError(c, http.StatusBadRequest, "invalid_request", "invalid clip range")
	case errors.Is(err, replay.ErrNoExactMatch):
		common.WriteError(c, http.StatusNotFound, "frame_not_found", "no frame sits exactly at the requested timestamp")
	case errors.Is(err, replay.ErrTimestampOutOfCoverage):
		common.WriteError(c, http.StatusNotFound, "timestamp_out_of_coverage", "requested timestamp falls outside the recording's stored coverage")
	case errors.Is(err, replay.ErrClipIntervalNotCovered):
		common.WriteError(c, http.StatusNotFound, "interval_not_covered", "the requested clip interval is not fully covered by the recording")
	case errors.Is(err, replay.ErrSidecarUnavailable):
		common.WriteError(c, http.StatusServiceUnavailable, "storage_unavailable", "media index is not available")
	case errors.Is(err, replay.ErrSidecarMalformed),
		errors.Is(err, replay.ErrSidecarUnsupported),
		errors.Is(err, replay.ErrSidecarEmpty),
		errors.Is(err, replay.ErrSidecarInvalidTimescale),
		errors.Is(err, replay.ErrSidecarInvalidSample),
		errors.Is(err, replay.ErrSidecarDuplicateSample),
		errors.Is(err, replay.ErrSidecarUnordered),
		errors.Is(err, replay.ErrSidecarMismatch):
		common.WriteError(c, http.StatusUnprocessableEntity, "invalid_media_index", "the recording's media index is invalid")
	case errors.Is(err, replay.ErrMediaUnavailable):
		common.WriteError(c, http.StatusServiceUnavailable, "storage_unavailable", "media storage is not available")
	case errors.Is(err, replay.ErrRecordingTooLarge):
		common.WriteError(c, http.StatusRequestEntityTooLarge, "recording_too_large", "recording exceeds the maximum size this service can extract from")
	case errors.Is(err, replay.ErrRecordingSizeUnknown):
		common.WriteError(c, http.StatusConflict, "recording_size_unknown", "recording size is unavailable for safe extraction")
	case errors.Is(err, replay.ErrExtractionFailed):
		common.WriteError(c, http.StatusServiceUnavailable, "extraction_failed", "media extraction failed")
	default:
		common.WriteInternalError(c)
	}
}
