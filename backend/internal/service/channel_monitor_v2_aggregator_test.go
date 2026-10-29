//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2MaxChunkForDepth(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	// Within last day → tightest ceiling (2h).
	require.Equal(t, channelMonitorV2MaxChunkNear1d, channelMonitorV2MaxChunkForDepth(now, now.Add(-2*time.Hour)))
	// Between 1d and 7d → 4h.
	require.Equal(t, channelMonitorV2MaxChunkNear7d, channelMonitorV2MaxChunkForDepth(now, now.Add(-2*24*time.Hour)))
	// Older than 7d → 6h (never 24h default).
	require.Equal(t, channelMonitorV2MaxChunkFar, channelMonitorV2MaxChunkForDepth(now, now.Add(-10*24*time.Hour)))
	require.Less(t, channelMonitorV2MaxChunkFar, 24*time.Hour)
	require.Equal(t, time.Hour, channelMonitorV2BackfillChunkInit)
	require.Equal(t, 15*time.Minute, channelMonitorV2MinBackfillChunk)
}

func TestChannelMonitorV2AggregatorAdaptiveChunk(t *testing.T) {
	s := NewChannelMonitorV2Aggregator(nil, nil, nil)
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	cursor := now.Add(-3 * time.Hour)

	// Failure shrinks only the historical chunk and schedules its next retry.
	s.backfillChunk = 2 * time.Hour
	s.recordBackfillFailure(now, cursor)
	require.Equal(t, time.Hour, s.backfillChunk)
	require.Equal(t, now.Add(time.Minute), s.backfillRetryAt)
	require.Equal(t, 1, s.backfillFailures)

	// Repeated failure halves again and delays the historical retry.
	s.recordBackfillFailure(now, cursor)
	require.Equal(t, 30*time.Minute, s.backfillChunk)
	require.Equal(t, now.Add(2*time.Minute), s.backfillRetryAt)

	// Fast success grows within depth ceiling and clears backoff.
	s.recordBackfillSuccess(cursor.Add(-30*time.Minute), 5*time.Second, now)
	require.Equal(t, 0, s.backfillFailures)
	require.True(t, s.backfillRetryAt.IsZero())
	require.Greater(t, s.backfillChunk, 30*time.Minute)
	require.LessOrEqual(t, s.backfillChunk, channelMonitorV2MaxChunkForDepth(now, cursor.Add(-30*time.Minute)))
}

type channelMonitorV2AggregationRange struct {
	start time.Time
	end   time.Time
}

type channelMonitorV2AggregatorRepo struct {
	channelMonitorV2RepoStub
	watermark *ChannelMonitorV2AggregationWatermark
	attempts  []channelMonitorV2AggregationRange
	committed []channelMonitorV2AggregationRange
	recompute func(context.Context, time.Time, time.Time) error
}

func (r *channelMonitorV2AggregatorRepo) GetAggregationWatermark(context.Context) (*ChannelMonitorV2AggregationWatermark, error) {
	if r.watermark == nil {
		return nil, nil
	}
	wm := *r.watermark
	return &wm, nil
}

func (r *channelMonitorV2AggregatorRepo) RecomputeRange(ctx context.Context, start, end time.Time) error {
	rangeValue := channelMonitorV2AggregationRange{start: start, end: end}
	r.attempts = append(r.attempts, rangeValue)
	if r.recompute != nil {
		if err := r.recompute(ctx, start, end); err != nil {
			return err
		}
	}
	r.committed = append(r.committed, rangeValue)
	if r.watermark == nil {
		r.watermark = &ChannelMonitorV2AggregationWatermark{}
	}
	r.watermark.HasData = true
	if end.After(r.watermark.DataThrough) {
		r.watermark.DataThrough = end
	}
	if r.watermark.BackfillCursor.IsZero() || start.Before(r.watermark.BackfillCursor) {
		r.watermark.BackfillCursor = start
	}
	return nil
}

func TestChannelMonitorV2AggregatorHistoricalFailuresKeepRecentAdvancing(t *testing.T) {
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	cursor := now.Add(-10 * 24 * time.Hour)
	repo := &channelMonitorV2AggregatorRepo{watermark: &ChannelMonitorV2AggregationWatermark{
		HasData: true, DataThrough: now.Add(-time.Minute), BackfillCursor: cursor,
	}}
	var historicalTicks []int
	tick := 0
	repo.recompute = func(ctx context.Context, start, end time.Time) error {
		if !end.After(cursor) {
			historicalTicks = append(historicalTicks, tick)
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "historical work must have a bounded query budget")
			require.Positive(t, time.Until(deadline))
			require.LessOrEqual(t, time.Until(deadline), 20*time.Second)
			return errors.New("historical query timed out")
		}
		return nil
	}
	s := NewChannelMonitorV2Aggregator(repo, nil, nil)
	for tick = 0; tick <= 26; tick++ {
		at := now.Add(time.Duration(tick) * time.Minute)
		s.runOnceAt(at)
		require.Len(t, repo.committed, tick+1, "every real-time tick must commit even while historical work backs off")
		require.Equal(t, at, repo.committed[tick].end)
		require.Equal(t, at, repo.watermark.DataThrough)
	}
	require.Equal(t, []int{0, 1, 3, 7, 15, 25}, historicalTicks)
	require.Equal(t, now.Add(35*time.Minute), s.backfillRetryAt)
	require.Equal(t, 6, s.backfillFailures)
	require.Equal(t, 15*time.Minute, s.backfillChunk)
	require.Equal(t, cursor, s.backfillAt, "failed historical work must not advance its cursor")
}

func TestChannelMonitorV2AggregatorMissedElevenMinutesStayContinuous(t *testing.T) {
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	repo := &channelMonitorV2AggregatorRepo{watermark: &ChannelMonitorV2AggregationWatermark{
		HasData: true, DataThrough: now.Add(-time.Minute), BackfillCursor: now.Add(-channelMonitorV2RetentionMax),
	}}
	s := NewChannelMonitorV2Aggregator(repo, nil, nil)
	s.runOnceAt(now)
	s.runOnceAt(now.Add(11 * time.Minute))
	require.Len(t, repo.committed, 2)
	require.Equal(t, channelMonitorV2AggregationRange{start: now.Add(-10 * time.Minute), end: now.Add(11 * time.Minute)}, repo.committed[1],
		"catch-up must cover the entire gap and revisit the previous batch's tail")
	require.Equal(t, now.Add(11*time.Minute), repo.watermark.DataThrough)
}

func TestChannelMonitorV2AggregatorFailureAndRestartCatchUpInBoundedRanges(t *testing.T) {
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	through := now.Add(-5 * time.Hour)
	repo := &channelMonitorV2AggregatorRepo{watermark: &ChannelMonitorV2AggregationWatermark{
		HasData: true, DataThrough: through, BackfillCursor: now.Add(-channelMonitorV2RetentionMax),
	}}
	failNext := true
	var lateLogAt time.Time
	lateLogReplayed := false
	repo.recompute = func(_ context.Context, start, end time.Time) error {
		if failNext {
			failNext = false
			return errors.New("database unavailable")
		}
		if !lateLogAt.IsZero() && !lateLogAt.Before(start) && lateLogAt.Before(end) {
			lateLogReplayed = true
		}
		return nil
	}
	s := NewChannelMonitorV2Aggregator(repo, nil, nil)
	s.runOnceAt(now)
	require.Empty(t, repo.committed)
	require.Equal(t, through, repo.watermark.DataThrough)
	require.Equal(t, through, s.recentThrough, "failed real-time work must not advance the in-memory cursor")
	require.False(t, s.recentSeeded)

	s.runOnceAt(now.Add(time.Minute))
	require.Len(t, repo.committed, 1)
	require.Equal(t, repo.attempts[0], repo.committed[0], "retry must start at the uncommitted watermark")
	require.Equal(t, channelMonitorV2AggregationRange{start: through.Add(-10 * time.Minute), end: through.Add(time.Hour + 50*time.Minute)}, repo.committed[0])
	require.False(t, lateLogReplayed)
	// This row arrives after the first batch committed, with a created_at in
	// that batch's final minute. The next batch must read that minute again.
	lateLogAt = repo.committed[0].end.Add(-time.Minute)

	// A new process must resume durable progress, even when it is still more
	// than two hours behind, instead of jumping straight to the newest tail.
	s = NewChannelMonitorV2Aggregator(repo, nil, nil)
	s.runOnceAt(now.Add(2 * time.Minute))
	require.Len(t, repo.committed, 2)
	require.True(t, lateLogReplayed, "restart catch-up must recover rows written after the preceding snapshot")
	lateLogAt = repo.committed[1].end.Add(-time.Minute)
	lateLogReplayed = false
	s.runOnceAt(now.Add(3 * time.Minute))
	require.True(t, lateLogReplayed, "later catch-up batches must also recover late rows from the preceding tail")
	require.Len(t, repo.committed, 3)
	for i, committed := range repo.committed {
		require.LessOrEqual(t, committed.end.Sub(committed.start), 2*time.Hour)
		if i > 0 {
			require.Equal(t, repo.committed[i-1].end.Add(-10*time.Minute), committed.start,
				"adjacent catch-up batches must overlap to recover late writes without leaving gaps")
		}
	}
	require.Equal(t, now.Add(3*time.Minute), repo.committed[2].end)
	require.Equal(t, now.Add(3*time.Minute), repo.watermark.DataThrough)
}

func TestChannelMonitorV2AggregatorHistoricalMinimumChunkAtMidnight(t *testing.T) {
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	cursor := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	repo := &channelMonitorV2AggregatorRepo{watermark: &ChannelMonitorV2AggregationWatermark{
		HasData: true, DataThrough: now.Add(-time.Minute), BackfillCursor: cursor,
	}}
	s := NewChannelMonitorV2Aggregator(repo, nil, nil)
	s.backfillChunk = 15 * time.Minute
	s.runOnceAt(now)
	require.Len(t, repo.committed, 2)
	require.Equal(t, channelMonitorV2AggregationRange{start: cursor.Add(-15 * time.Minute), end: cursor}, repo.committed[1],
		"an old midnight cursor must not expand the minimum retry into an entire day")
	require.Equal(t, cursor.Add(-15*time.Minute), s.backfillAt)
}

func TestChannelMonitorV2AggregatorSeedsRecentTwoHours(t *testing.T) {
	now := time.Date(2026, 10, 29, 8, 0, 37, 0, time.FixedZone("UTC+8", 8*60*60))
	end := now.UTC().Truncate(time.Minute)
	for _, test := range []struct {
		name      string
		watermark *ChannelMonitorV2AggregationWatermark
	}{
		{name: "empty repository"},
		{name: "existing watermark replays recent holes", watermark: &ChannelMonitorV2AggregationWatermark{
			HasData: true, DataThrough: end.Add(-time.Minute), BackfillCursor: end.Add(-channelMonitorV2RetentionMax),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &channelMonitorV2AggregatorRepo{watermark: test.watermark}
			s := NewChannelMonitorV2Aggregator(repo, nil, nil)
			s.runOnceAt(now)
			require.Equal(t, []channelMonitorV2AggregationRange{{start: end.Add(-2 * time.Hour), end: end}}, repo.committed)
			require.Equal(t, end, s.recentThrough)
			require.True(t, s.recentSeeded)
			require.Equal(t, end, repo.watermark.DataThrough)
		})
	}
}
