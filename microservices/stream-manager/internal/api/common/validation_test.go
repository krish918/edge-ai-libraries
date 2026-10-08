// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package common

import (
	"testing"
	"time"
)

func TestParseTimestampAcceptsCanonicalUTC(t *testing.T) {
	got, err := ParseTimestamp("timestamp", "2026-10-07T12:30:00.123456789Z")
	if err != nil {
		t.Fatalf("ParseTimestamp: %v", err)
	}
	want := time.Date(2026, time.October, 7, 12, 30, 0, 123456789, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("timestamp = %s, want %s in UTC", got, want)
	}
}

func TestParseTimestampRemainsUTCOnly(t *testing.T) {
	if _, err := ParseTimestamp("start_ts", "2026-10-07T12:30:00-05:00"); err == nil {
		t.Fatal("ParseTimestamp accepted a non-UTC offset")
	}
}

func TestParseTimestampRejectsMoreThanNineFractionDigits(t *testing.T) {
	if _, err := ParseTimestamp("timestamp", "2026-10-07T12:30:00.1234567890Z"); err == nil {
		t.Fatal("ParseTimestamp accepted more than nine fractional digits")
	}
}

func TestCheckBufferLengthBounds(t *testing.T) {
	for _, seconds := range []int{5, 300} {
		if err := CheckBufferLength(seconds); err != nil {
			t.Errorf("CheckBufferLength(%d): %v", seconds, err)
		}
	}
	for _, seconds := range []int{4, 301} {
		if err := CheckBufferLength(seconds); err == nil {
			t.Errorf("CheckBufferLength(%d) succeeded, want out-of-range error", seconds)
		}
	}
}
