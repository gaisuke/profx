package services

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrDemoQuotaExceeded is returned when a public deployment has spent its daily
// evaluation budget.
var ErrDemoQuotaExceeded = errors.New("demo quota exhausted for today")

// JobCounter counts evaluations created since a point in time.
type JobCounter interface {
	CountCreatedSince(since time.Time) (int, error)
}

// DemoMode protects a publicly reachable deployment. Every evaluation costs
// model calls, so an open endpoint is an open wallet; a daily cap makes the
// worst case bounded and knowable. The quota is reported rather than hidden,
// because a limit nobody can see is indistinguishable from a broken app.
type DemoMode struct {
	counter   JobCounter
	maxPerDay int
	location  *time.Location
}

func NewDemoMode(counter JobCounter, maxPerDay int) *DemoMode {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*60*60)
	}
	return &DemoMode{counter: counter, maxPerDay: maxPerDay, location: loc}
}

func (d *DemoMode) Enabled() bool { return d.maxPerDay > 0 }

func (d *DemoMode) Limit() int { return d.maxPerDay }

// DayStart is when the quota resets: midnight in the deployment's timezone, so
// the number a visitor sees matches the day they are living in.
func (d *DemoMode) DayStart(now time.Time) time.Time {
	local := now.In(d.location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, d.location)
}

type DemoStatus struct {
	Enabled   bool   `json:"enabled"`
	Used      int    `json:"used"`
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	ResetsAt  string `json:"resets_at"`
}

// Allow reports how many evaluations are left, and refuses once the cap is hit.
func (d *DemoMode) Allow(ctx context.Context) (int, error) {
	status, err := d.Status(ctx)
	if err != nil {
		return 0, err
	}
	if status.Enabled && status.Used >= status.Limit {
		return 0, fmt.Errorf("%w (%d of %d used, resets %s)", ErrDemoQuotaExceeded, status.Used, status.Limit, status.ResetsAt)
	}
	return status.Remaining, nil
}

func (d *DemoMode) Status(ctx context.Context) (DemoStatus, error) {
	now := time.Now()
	start := d.DayStart(now)
	status := DemoStatus{
		Enabled:  d.Enabled(),
		Limit:    d.maxPerDay,
		ResetsAt: start.Add(24 * time.Hour).Format(time.RFC3339),
	}
	if !d.Enabled() {
		status.Remaining = 0
		return status, nil
	}
	used, err := d.counter.CountCreatedSince(start)
	if err != nil {
		return status, fmt.Errorf("count today's evaluations: %w", err)
	}
	status.Used = used
	status.Remaining = d.maxPerDay - used
	if status.Remaining < 0 {
		status.Remaining = 0
	}
	return status, nil
}
