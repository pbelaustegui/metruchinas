package itcrm

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSource struct {
	calls int
	value float64
	err   error
}

func (f *fakeSource) fetch(context.Context) (Indicator, error) {
	f.calls++
	if f.err != nil {
		return Indicator{}, f.err
	}
	return Indicator{Value: f.value}, nil
}

func newTestCache(src *fakeSource, now *time.Time) *Cache {
	c := NewDailyCache(src.fetch)
	c.Now = func() time.Time { return *now }
	return c
}

func TestCacheServesFromMemoryUntilNextLocalMidnight(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	src := &fakeSource{value: 85}
	c := newTestCache(src, &now)

	for i := 0; i < 3; i++ {
		got, err := c.Get(context.Background(), false)
		if err != nil || got.Value != 85 {
			t.Fatalf("Get() = %v, %v, want 85, nil", got, err)
		}
	}
	if src.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1 within the same day", src.calls)
	}

	now = time.Date(2026, 10, 2, 0, 0, 1, 0, time.UTC)
	if _, err := c.Get(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if src.calls != 2 {
		t.Fatalf("fetch calls = %d, want 2 after midnight", src.calls)
	}
}

func TestCacheForceBypassesCache(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	src := &fakeSource{value: 85}
	c := newTestCache(src, &now)
	_, _ = c.Get(context.Background(), false)
	_, _ = c.Get(context.Background(), true)
	if src.calls != 2 {
		t.Fatalf("fetch calls = %d, want 2 with force", src.calls)
	}
}

func TestCacheFailureReturnsErrorAndBacksOff(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	boom := errors.New("bcra down")
	src := &fakeSource{err: boom}
	c := newTestCache(src, &now)

	for i := 0; i < 2; i++ {
		if _, err := c.Get(context.Background(), false); !errors.Is(err, boom) {
			t.Fatalf("Get() error = %v, want %v", err, boom)
		}
	}
	if src.calls != 1 {
		t.Fatalf("fetch calls = %d, want 1 inside the retry window", src.calls)
	}

	now = now.Add(failureRetryInterval + time.Second)
	src.err, src.value = nil, 86
	got, err := c.Get(context.Background(), false)
	if err != nil || got.Value != 86 {
		t.Fatalf("Get() after retry window = %v, %v, want 86, nil", got, err)
	}
}

func TestFailureBackoffDoublesUpToCeiling(t *testing.T) {
	if got := failureBackoff(1); got != failureRetryInterval {
		t.Errorf("failureBackoff(1) = %v", got)
	}
	if got := failureBackoff(2); got != 2*failureRetryInterval {
		t.Errorf("failureBackoff(2) = %v", got)
	}
	if got := failureBackoff(50); got != failureRetryCeiling {
		t.Errorf("failureBackoff(50) = %v, want ceiling", got)
	}
}
