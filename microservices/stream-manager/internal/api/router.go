// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/api/common"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/api/handler"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/logging"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/mediaaccess"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/record"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/replay"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/storage"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/stream"
)

var _ handler.RecordingService = (*record.Service)(nil)

// NewRouter builds the HTTP API as an http.Handler. version is returned by
// GET /v1/version, and logger receives one access entry per request. Filesystem
// media requires mediaSigner; nil disables /v1/media.
func NewRouter(service *replay.RetrievalService, media storage.MediaStore, version string, logger *slog.Logger, mediaSigner *mediaaccess.Signer, buffers stream.Bufferer, recordings *record.Service) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(logging.Middleware(logger), logging.Recovery(), common.BodyLimit())

	retrievalHandler := handler.NewRetrievalHandler(service, media)
	mediaHandler := handler.NewMediaHandler(media, mediaSigner)

	router.GET("/v1/health", handler.Health)
	router.GET("/v1/version", handler.Version(version))
	router.GET("/v1/replays/:recording_id/frame", retrievalHandler.GetFrame)
	router.GET("/v1/replays/:recording_id/frame/url", retrievalHandler.GetFrameURL)
	router.GET("/v1/replays/:recording_id/clip", retrievalHandler.GetClip)
	router.GET("/v1/replays/:recording_id/clip/url", retrievalHandler.GetClipURL)
	router.GET("/v1/media/:token", mediaHandler.GetMedia)

	streams := handler.StreamHandler{Buffers: buffers}
	router.POST("/v1/streams", streams.Create)
	router.GET("/v1/streams", streams.List)
	streamRoutes := router.Group("/v1/streams/:stream-id", common.RequireID("stream-id", "stream_not_found", "stream not found"))
	streamRoutes.GET("", streams.Get)
	streamRoutes.DELETE("", streams.Delete)
	streamRoutes.PUT("/buffer", streams.UpdateBuffer)

	var recordingService handler.RecordingService
	if recordings != nil {
		recordingService = recordings
	}
	records := handler.RecordHandler{Records: recordingService}
	router.POST("/v1/records/start", records.Start)
	router.POST("/v1/records/stop", records.Stop)
	router.GET("/v1/records", records.List)
	recordRoutes := router.Group("/v1/records/:recording-id", common.RequireID("recording-id", "record_not_found", "record not found"))
	recordRoutes.GET("", records.Get)
	recordRoutes.DELETE("", records.Delete)

	router.GET("/docs/v1/*any", handler.ServeSwaggerUI)

	return router
}
