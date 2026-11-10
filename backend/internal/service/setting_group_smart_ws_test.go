package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type smartSettingsStub struct {
	SettingRepository
	mu      sync.Mutex
	raw     string
	err     error
	started chan struct{}
	resume  chan struct{}
}

func (r *smartSettingsStub) GetValue(context.Context, string) (string, error) {
	r.mu.Lock()
	raw, err, started, resume := r.raw, r.err, r.started, r.resume
	r.started = nil
	r.resume = nil
	r.mu.Unlock()
	if started != nil {
		close(started)
		<-resume
	}
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", ErrSettingNotFound
	}
	return raw, nil
}
func (r *smartSettingsStub) CompareAndSwapGroupSmartWS(_ context.Context, _ int64, _ string, previous *string, next string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous == nil && r.raw != "" || previous != nil && *previous != r.raw {
		return false, nil
	}
	r.raw = next
	return true, nil
}
func TestGroupSmartWSDefaultCASAndFailure(t *testing.T) {
	r := &smartSettingsStub{}
	s := &SettingService{settingRepo: r, defaultSubGroupReader: &smartGroupReaderStub{byID: map[int64]*Group{1: {ID: 1, Platform: PlatformOpenAI}}}}
	ctx := context.Background()
	require.False(t, s.GroupSmartWSEnabled(ctx, 1))
	p, err := s.UpdateGroupSmartWS(ctx, 1, true, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.Revision)
	require.True(t, s.GroupSmartWSEnabled(ctx, 1))
	_, err = s.UpdateGroupSmartWS(ctx, 1, false, 0)
	require.ErrorIs(t, err, ErrGroupSmartWSConflict)
	s.groupSmartWSMu.Lock()
	s.groupSmartWSCache[1] = groupSmartWSCacheEntry{enabled: true, expires: time.Now().Add(-time.Second)}
	s.groupSmartWSMu.Unlock()
	r.err = errors.New("db unavailable")
	require.False(t, s.GroupSmartWSEnabled(ctx, 1))
	for _, raw := range []string{`{"enabled":true}`, `{"enabled":true,"revision":0}`, `{"enabled":true,"revision":1,"unknown":1}`} {
		_, err := decodeGroupSmartWS(raw)
		require.Error(t, err)
	}
}
func TestGroupSmartWSInvalidatesConcurrentRead(t *testing.T) {
	r := &smartSettingsStub{raw: `{"enabled":true,"revision":1}`, started: make(chan struct{}), resume: make(chan struct{})}
	started, resume := r.started, r.resume
	s := &SettingService{settingRepo: r, defaultSubGroupReader: &smartGroupReaderStub{byID: map[int64]*Group{1: {ID: 1, Platform: PlatformOpenAI}}}}
	done := make(chan bool, 1)
	go func() { done <- s.GroupSmartWSEnabled(context.Background(), 1) }()
	<-started
	_, err := s.UpdateGroupSmartWS(context.Background(), 1, false, 1)
	require.NoError(t, err)
	close(resume)
	require.False(t, <-done)
	require.False(t, s.GroupSmartWSEnabled(context.Background(), 1))
}
func TestSmartWSAttemptStopsReplay(t *testing.T) {
	_, attempt := beginGroupSmartWSAttempt(context.Background(), nil)
	cause := errors.New("stream broke")
	require.Same(t, cause, finishGroupSmartWSAttempt(attempt, cause))
	attempt.sent.Store(true)
	err := finishGroupSmartWSAttempt(attempt, cause)
	var transport *PluginTransportError
	require.ErrorAs(t, err, &transport)
	require.True(t, transport.RequestSent)
}

type smartGroupReaderStub struct{ byID map[int64]*Group }

func (r *smartGroupReaderStub) GetByID(_ context.Context, id int64) (*Group, error) {
	return r.byID[id], nil
}
