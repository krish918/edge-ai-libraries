// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/api/common"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/model"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/record"
)

type recordingServiceStub struct {
	startCalls int
	start      record.StartOptions
	startRec   model.Recording
	stopCalls  int
	stopID     string
	stopRec    model.Recording
	accepted   bool
}

var _ RecordingService = (*recordingServiceStub)(nil)

func (s *recordingServiceStub) Start(_ context.Context, options record.StartOptions) (model.Recording, error) {
	s.startCalls++
	s.start = options
	return s.startRec, nil
}

func (s *recordingServiceStub) Stop(_ context.Context, recordingID string) (model.Recording, bool, error) {
	s.stopCalls++
	s.stopID = recordingID
	return s.stopRec, s.accepted, nil
}

func (*recordingServiceStub) List(context.Context, model.RecordingFilter) ([]model.Recording, string, error) {
	return nil, "", nil
}

func (*recordingServiceStub) Get(context.Context, string) (model.Recording, error) {
	return model.Recording{}, nil
}

func (*recordingServiceStub) DeleteRecording(context.Context, string) error { return nil }

func newRecordingRouter(service RecordingService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := RecordHandler{Records: service}
	router.POST("/v1/records/start", handler.Start)
	router.POST("/v1/records/stop", handler.Stop)
	return router
}

func testRecording(id, state string) model.Recording {
	return model.Recording{
		RecordingID: id,
		SensorID:    "sensor-one",
		StreamID:    "str-one",
		State:       state,
		StartTS:     time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC),
		CreationTS:  time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC),
		Metadata:    map[string]any{},
	}
}

func TestRecordStartRouteCreatesOneRecording(t *testing.T) {
	service := &recordingServiceStub{startRec: testRecording("rec-one", model.RecordingStateRecording)}
	router := newRecordingRouter(service)
	req := httptest.NewRequest(http.MethodPost, "/v1/records/start", strings.NewReader(
		`{"stream_id":"str-one","start_ts":"2026-10-04T12:00:00Z","duration":2.5,"pre_event_duration":3,"metadata":{"event":"inspection"}}`,
	))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
	if service.startCalls != 1 || service.start.StreamID != "str-one" {
		t.Fatalf("Start calls/options = %d/%+v", service.startCalls, service.start)
	}
	if service.start.Duration == nil || *service.start.Duration != 2500*time.Millisecond || service.start.PreEvent != 3*time.Second {
		t.Fatalf("recording durations = duration %v, pre-event %s", service.start.Duration, service.start.PreEvent)
	}
	if service.start.Metadata["event"] != "inspection" {
		t.Fatalf("metadata = %v", service.start.Metadata)
	}
	var body recordStartResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Recordings) != 1 || body.Recordings[0].RecordingID != "rec-one" || body.Recordings[0].State != model.RecordingStateRecording {
		t.Fatalf("response recordings = %+v", body.Recordings)
	}
}

func TestRecordStopRouteReturnsAcceptedWhenFinalizing(t *testing.T) {
	service := &recordingServiceStub{
		stopRec:  testRecording("rec-open", model.RecordingStateFinalizing),
		accepted: true,
	}
	router := newRecordingRouter(service)
	req := httptest.NewRequest(http.MethodPost, "/v1/records/stop", strings.NewReader(`{"recording_id":"rec-open"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", response.Code, response.Body.String())
	}
	if service.stopCalls != 1 || service.stopID != "rec-open" {
		t.Fatalf("Stop calls/id = %d/%q", service.stopCalls, service.stopID)
	}
	var body recordingResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RecordingID != "rec-open" || body.State != model.RecordingStateFinalizing {
		t.Fatalf("response = %+v", body)
	}
}

func TestRecordStartRouteRejectsLegacySelectorArrays(t *testing.T) {
	for _, selector := range []string{"stream_ids", "sensor_ids"} {
		t.Run(selector, func(t *testing.T) {
			service := &recordingServiceStub{}
			router := newRecordingRouter(service)
			body := `{"` + selector + `":["str-one"],"start_ts":"2026-10-04T12:00:00Z"}`
			req := httptest.NewRequest(http.MethodPost, "/v1/records/start", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
			}
			if service.startCalls != 0 {
				t.Fatalf("Start called %d times for rejected selector", service.startCalls)
			}
			var apiError model.ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if apiError.ErrorCode != "invalid_request" {
				t.Fatalf("error code = %q", apiError.ErrorCode)
			}
		})
	}
}

func TestRecordStartRequestAcceptsOneStreamID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/records/start", strings.NewReader(
		`{"stream_id":"str-one","start_ts":"2026-10-04T12:00:00Z"}`,
	))

	var req recordStartRequest
	if err := common.DecodeJSON(ctx, &req); err != nil {
		t.Fatalf("decode single-stream request: %v", err)
	}
	startTS, code, err := req.validate(time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("validate single-stream request (%s): %v", code, err)
	}
	if req.StreamID != "str-one" || !startTS.Equal(time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("decoded request = %+v, start = %s", req, startTS)
	}
}

func TestRecordStartRequestRejectsSelectorArrays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/records/start", strings.NewReader(
		`{"stream_ids":["str-one"],"start_ts":"2026-10-04T12:00:00Z"}`,
	))

	var req recordStartRequest
	if err := common.DecodeJSON(ctx, &req); err == nil {
		t.Fatal("decode accepted stream_ids; want unknown-field error")
	}
}
