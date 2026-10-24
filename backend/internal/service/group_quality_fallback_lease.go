package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Renewal keeps each fallback request's in-flight tracking lease alive for
// long streams. Independent fallback requests may hold leases concurrently.
type GroupQualityFallbackLeaseGate interface {
	RefreshGroupQualityFallback(context.Context, string) (bool, error)
}

var ErrGroupQualityFallbackLeaseLost = errors.New("group quality fallback lease lost")

const groupQualityFallbackRefreshInterval = 30 * time.Second

// GroupQualityFallbackActive follows the live attempt slot even when the
// forwarding path does not carry the scheduler's fallback marker. It remains
// true after release so cancellation cannot accidentally detach that upstream.
func GroupQualityFallbackActive(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	if slot, ok := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot); ok {
		slot.mu.Lock()
		defer slot.mu.Unlock()
		return slot.fallbackActive
	}
	return false
}

func cancelGroupQualityTraffic(ctx context.Context, cause error) {
	if slot, ok := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot); ok {
		slot.mu.Lock()
		cancel := slot.cancel
		slot.mu.Unlock()
		if cancel != nil {
			cancel(cause)
		}
	}
}

func bindGroupQualityFallbackStop(ctx context.Context, lease string, stop func()) {
	if slot, ok := ctx.Value(groupQualityTrafficKey{}).(*groupQualityTrafficSlot); ok {
		slot.mu.Lock()
		defer slot.mu.Unlock()
		if slot.request.LeaseID == lease {
			slot.stopFallback = stop
			slot.fallbackActive = true
		}
	}
}

func startGroupQualityFallbackLease(ctx context.Context, gate GroupQualityFallbackLeaseGate, lease string, interval time.Duration) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				owned, err := gate.RefreshGroupQualityFallback(refreshCtx, lease)
				cancel()
				if ctx.Err() != nil {
					return
				}
				if err != nil || !owned {
					cancelGroupQualityTraffic(ctx, ErrGroupQualityFallbackLeaseLost)
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }); <-done }
}
