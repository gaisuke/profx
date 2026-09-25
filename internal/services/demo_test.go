package services

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeCounter struct {
	n   int
	err error
}

func (f fakeCounter) CountCreatedSince(time.Time) (int, error) { return f.n, f.err }

func TestDemoModeDisabledByDefault(t *testing.T) {
	d := NewDemoMode(fakeCounter{n: 999}, 0)
	if d.Enabled() {
		t.Fatal("a private deployment must not be capped")
	}
	if remaining, err := d.Allow(context.Background()); err != nil || remaining != 0 {
		t.Errorf("Allow on a disabled demo = (%d, %v), want (0, nil)", remaining, err)
	}
}

// The quota resets at midnight in the deployment's timezone, so the number a
// visitor sees matches their own day.
func TestDemoQuotaResetsAtMidnightWIB(t *testing.T) {
	d := NewDemoMode(fakeCounter{}, 10)

	wib := time.FixedZone("WIB", 7*60*60)
	justBefore := time.Date(2026, 9, 26, 23, 59, 0, 0, wib)
	start := d.DayStart(justBefore)
	if start.Day() != 26 || start.Hour() != 0 {
		t.Errorf("DayStart(%v) = %v, want midnight of the 26th", justBefore, start)
	}

	afterMidnight := time.Date(2026, 9, 27, 0, 1, 0, 0, wib)
	if got := d.DayStart(afterMidnight).Day(); got != 27 {
		t.Errorf("DayStart just after midnight = day %d, want 27", got)
	}
}

func TestDemoAllowReportsRemainingAndRefusesAtTheCap(t *testing.T) {
	d := NewDemoMode(fakeCounter{n: 7}, 10)

	remaining, err := d.Allow(context.Background())
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if remaining != 3 {
		t.Errorf("remaining = %d, want 3", remaining)
	}

	exhausted := NewDemoMode(fakeCounter{n: 10}, 10)
	if _, err := exhausted.Allow(context.Background()); !errors.Is(err, ErrDemoQuotaExceeded) {
		t.Fatalf("Allow at the cap = %v, want ErrDemoQuotaExceeded", err)
	}

	status, err := exhausted.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Used != 10 || status.Limit != 10 || status.Remaining != 0 || !status.Enabled {
		t.Errorf("status at the cap = %+v", status)
	}
	if status.ResetsAt == "" {
		t.Error("a quota must tell the caller when it resets")
	}

	over := NewDemoMode(fakeCounter{n: 42}, 10)
	status, err = over.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Remaining != 0 {
		t.Errorf("remaining must never go negative, got %d", status.Remaining)
	}
}

// A broken counter must not silently allow unlimited spending.
func TestDemoAllowPropagatesCounterFailure(t *testing.T) {
	d := NewDemoMode(fakeCounter{err: errors.New("db down")}, 10)
	if _, err := d.Allow(context.Background()); err == nil || errors.Is(err, ErrDemoQuotaExceeded) {
		t.Fatalf("Allow = %v, want a counter error that is not a quota refusal", err)
	}
}
