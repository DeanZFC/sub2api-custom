package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/qqbot"
	"github.com/stretchr/testify/require"
)

type qqPriceChannelStub struct {
	channels []Channel
	err      error
}

func (s *qqPriceChannelStub) ListAll(context.Context) ([]Channel, error) { return s.channels, s.err }
func priceField(t *testing.T, fields []qqbot.PriceField, key string) qqbot.PriceField {
	t.Helper()
	for _, f := range fields {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("missing field %s", key)
	return qqbot.PriceField{}
}

func TestQQPriceSnapshotUnitsAndTimeBoundaries(t *testing.T) {
	input, perRequest := 0.000002, 0.05
	at := time.Date(2026, 10, 20, 0, 59, 59, 0, time.UTC)
	pricing := ChannelModelPricing{Platform: "openai", Models: []string{"example"}, InputPrice: &input, PerRequestPrice: &perRequest, TimePricing: &ChannelTimePricing{Timezone: "Asia/Shanghai", WeekdaysOnly: true, Periods: []ChannelTimePricingPeriod{{StartTime: "09:00:00", EndTime: "09:00:01", Multiplier: .5}}}}
	p := qqBotPriceSource{channels: &qqPriceChannelStub{channels: []Channel{{ID: 1, Status: StatusActive, Name: "channel", ModelPricing: []ChannelModelPricing{pricing}}, {ID: 2, Status: StatusDisabled}}}, groups: &qqStatusGroupStub{groups: []Group{{ID: 1, Name: "group", Status: StatusActive, RateMultiplier: .8, SubscriptionType: SubscriptionTypeSubscription, PeakRateEnabled: true, PeakStart: "09:00", PeakEnd: "10:00", PeakRateMultiplier: 2}}}}
	before, err := p.PriceSnapshot(context.Background(), at)
	require.NoError(t, err)
	require.Len(t, before.States, 2)
	require.Equal(t, "USD 2", priceField(t, before.States[0].Fields, "openai/example/01_input").Value)
	require.Equal(t, "USD 0.05", priceField(t, before.States[0].Fields, "openai/example/08_request").Value)
	require.WithinDuration(t, at.Add(time.Second), before.NextCheck, time.Nanosecond)
	inside, err := p.PriceSnapshot(context.Background(), at.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, "0.5×", priceField(t, inside.States[0].Fields, "openai/example/31_time_current").Value)
	require.WithinDuration(t, at.Add(2*time.Second), inside.NextCheck, time.Nanosecond)
	after, err := p.PriceSnapshot(context.Background(), at.Add(2*time.Second))
	require.NoError(t, err)
	require.Equal(t, "1×", priceField(t, after.States[0].Fields, "openai/example/31_time_current").Value)
	saturday := time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)
	weekend, err := p.PriceSnapshot(context.Background(), saturday)
	require.NoError(t, err)
	require.Equal(t, "1×", priceField(t, weekend.States[0].Fields, "openai/example/31_time_current").Value)
}

func TestQQPriceSnapshotIgnoresInternalCostAndRowIDs(t *testing.T) {
	price := 1e-6
	pricing := ChannelModelPricing{Platform: "openai", Models: []string{"example"}, InputPrice: &price}
	repo := &qqPriceChannelStub{channels: []Channel{{ID: 1, Name: "channel", Status: StatusActive, ModelPricing: []ChannelModelPricing{pricing}}}}
	p := qqBotPriceSource{channels: repo, groups: &qqStatusGroupStub{}}
	at := time.Now()
	before, err := p.PriceSnapshot(context.Background(), at)
	require.NoError(t, err)
	repo.channels[0].ModelPricing[0].ID = 999
	repo.channels[0].ModelPricing[0].UpdatedAt = time.Now()
	repo.channels[0].AccountStatsPricingRules = []AccountStatsPricingRule{{Name: "internal cost", Pricing: []ChannelModelPricing{{InputPrice: &price}}}}
	after, err := p.PriceSnapshot(context.Background(), at)
	require.NoError(t, err)
	require.Equal(t, before, after)
	repo.err = errors.New("unavailable")
	_, err = p.PriceSnapshot(context.Background(), at)
	require.Error(t, err)
}

func TestQQNextBoundaryMidnightAndGroupPeak(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	at := time.Date(2026, 10, 23, 23, 59, 59, 500000000, loc)
	require.Equal(t, time.Date(2026, 10, 24, 0, 0, 0, 0, loc), qqNextBoundary(at, at.Add(15*time.Second), loc, "00:00"))
	group := Group{ID: 1, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription, RateMultiplier: .8, PeakRateEnabled: true, PeakStart: "09:00", PeakEnd: "10:00", PeakRateMultiplier: 2}
	p := qqBotPriceSource{channels: &qqPriceChannelStub{}, groups: &qqStatusGroupStub{groups: []Group{group}}}
	for _, now := range []time.Time{at, time.Date(2026, 10, 20, 9, 0, 0, 0, loc), time.Date(2026, 10, 20, 10, 0, 0, 0, loc)} {
		got, err := p.PriceSnapshot(context.Background(), now)
		require.NoError(t, err)
		require.Equal(t, qqMultiplier(group.RateMultiplier*group.PeakMultiplierAt(now)), priceField(t, got.States[0].Fields, "05_current").Value)
	}
}
