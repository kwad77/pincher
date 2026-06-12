// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// retryBusy (#2022) turns cross-process SQLITE_BUSY hard-failures on
// short writes into bounded-latency retries. These tests exercise the
// helper directly with synthetic fn closures — no real database, so they
// stay fast and deterministic.

func TestRetryBusy_SucceedsImmediately(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return nil
	}

	err := retryBusy(context.Background(), fn)

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected call count 1, got %d", callCount)
	}
}

func TestRetryBusy_RetriesThenSucceeds(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		if callCount <= 2 {
			return errors.New("database is locked (5)")
		}
		return nil
	}

	err := retryBusy(context.Background(), fn)

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected call count 3, got %d", callCount)
	}
}

func TestRetryBusy_NonBusyErrorReturnsImmediately(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return errors.New("boom")
	}

	err := retryBusy(context.Background(), fn)

	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected error to contain 'boom', got %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected call count 1, got %d", callCount)
	}
}

func TestRetryBusy_RespectsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	callCount := 0
	fn := func() error {
		callCount++
		return errors.New("database is locked (5)")
	}

	start := time.Now()
	err := retryBusy(ctx, fn)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("expected elapsed time < 2s, got %v", elapsed)
	}
	if callCount < 2 {
		t.Fatalf("expected call count >= 2, got %d", callCount)
	}
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected deadline/context cause in error, got %v", err)
	}
}
