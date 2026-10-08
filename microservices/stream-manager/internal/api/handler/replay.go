// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/api/common"
	"github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/internal/model"
)

type frameQuery struct {
	RecordingID string
	Timestamp   string
	Format      string
	Match       string
}

func (r frameQuery) validate() (model.FrameRequest, string, error) {
	if strings.TrimSpace(r.RecordingID) == "" {
		return model.FrameRequest{}, "invalid_request", errors.New("missing recording_id")
	}
	startTS, err := common.ParseTimestamp("timestamp", r.Timestamp)
	if err != nil {
		return model.FrameRequest{}, "invalid_timestamp", err
	}
	return model.FrameRequest{
		RecordingID: r.RecordingID,
		StartTS:     startTS,
		Format:      r.Format,
		Match:       r.Match,
	}, "", nil
}

type clipQuery struct {
	RecordingID string
	StartTS     string
	EndTS       string
	Duration    string
	Format      string
}

func (r clipQuery) validate() (model.ClipRequest, string, error) {
	if strings.TrimSpace(r.RecordingID) == "" {
		return model.ClipRequest{}, "invalid_request", errors.New("missing recording_id")
	}
	startTS, err := common.ParseTimestamp("start_ts", r.StartTS)
	if err != nil {
		return model.ClipRequest{}, "invalid_timestamp", err
	}
	endProvided, durationProvided := r.EndTS != "", r.Duration != ""
	if endProvided == durationProvided {
		if endProvided {
			return model.ClipRequest{}, "invalid_request", errors.New("provide exactly one of end_ts or duration")
		}
		return model.ClipRequest{}, "invalid_request", errors.New("missing clip end boundary")
	}
	var endTS *time.Time
	if endProvided {
		parsed, err := common.ParseTimestamp("end_ts", r.EndTS)
		if err != nil {
			return model.ClipRequest{}, "invalid_timestamp", err
		}
		endTS = &parsed
	}
	var clipDuration *float64
	if durationProvided {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(r.Duration), 64)
		if err != nil {
			return model.ClipRequest{}, "invalid_duration", errors.New("duration must be a number")
		}
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return model.ClipRequest{}, "invalid_duration", errors.New("duration must be finite")
		}
		if parsed <= 0 {
			return model.ClipRequest{}, "invalid_duration", errors.New("duration must be greater than zero")
		}
		clipDuration = &parsed
	}
	return model.ClipRequest{
		RecordingID:  r.RecordingID,
		StartTS:      startTS,
		EndTS:        endTS,
		ClipDuration: clipDuration,
		Format:       r.Format,
	}, "", nil
}
