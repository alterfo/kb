package transport

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfterHTTPDate(t *testing.T) {
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	d := ParseRetryAfter(future)
	if d <= 0 || d > 10*time.Second {
		t.Fatalf("ParseRetryAfter(%q) = %v, want ~3s", future, d)
	}
}

func TestParseRetryAfterPastDateIsZero(t *testing.T) {
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	if d := ParseRetryAfter(past); d != 0 {
		t.Fatalf("ParseRetryAfter(past date) = %v, want 0", d)
	}
}

func TestParseRetryAfterNegativeOrEmptyIsZero(t *testing.T) {
	if d := ParseRetryAfter("-5"); d != 0 {
		t.Fatalf("ParseRetryAfter(-5) = %v, want 0", d)
	}
	if d := ParseRetryAfter(""); d != 0 {
		t.Fatalf("ParseRetryAfter(empty) = %v, want 0", d)
	}
	if d := ParseRetryAfter("not-a-date"); d != 0 {
		t.Fatalf("ParseRetryAfter(garbage) = %v, want 0", d)
	}
}

func TestDefaultSleepRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := DefaultSleep(ctx, time.Minute); err != context.Canceled {
		t.Fatalf("DefaultSleep(canceled) = %v, want context.Canceled", err)
	}
}

func TestDefaultSleepZeroDelayReturnsImmediately(t *testing.T) {
	if err := DefaultSleep(context.Background(), 0); err != nil {
		t.Fatalf("DefaultSleep(0) = %v, want nil", err)
	}
}
