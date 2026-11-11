//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAdminService_CreateGroupForcedServiceTier(t *testing.T) {
	for _, tt := range []struct {
		name, platform string
		tier           *string
		legacy         bool
		want           string
	}{
		{name: "off", platform: PlatformOpenAI},
		{name: "legacy fast", platform: PlatformOpenAI, legacy: true, want: "priority"},
		{name: "explicit off overrides legacy", platform: PlatformOpenAI, tier: ptrString(""), legacy: true},
		{name: "priority", platform: PlatformOpenAI, tier: ptrString("priority"), want: "priority"},
		{name: "ultrafast overrides legacy", platform: PlatformOpenAI, tier: ptrString("ultrafast"), legacy: true, want: "ultrafast"},
		{name: "composite", platform: PlatformComposite, tier: ptrString("ultrafast"), want: "ultrafast"},
		{name: "unsupported platform", platform: PlatformAnthropic, tier: ptrString("ultrafast")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{}
			svc := &adminServiceImpl{groupRepo: repo}
			group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
				Name: tt.name, Platform: tt.platform, RateMultiplier: 1,
				ForceOpenAIServiceTier: tt.tier, ForceOpenAIFast: tt.legacy,
			})
			require.NoError(t, err)
			require.Equal(t, tt.want, repo.created.ForceOpenAIServiceTier)
			require.Equal(t, tt.want == "priority", group.ForceOpenAIFast)
		})
	}
}

func TestAdminService_UpdateGroupForcedServiceTier(t *testing.T) {
	truth, falsity := true, false
	for _, tt := range []struct {
		name, platform, want string
		tier                 *string
		legacy               *bool
	}{
		{name: "omitted retains ultrafast", want: "ultrafast"},
		{name: "legacy enable", legacy: &truth, want: "priority"},
		{name: "legacy disable", legacy: &falsity},
		{name: "explicit off overrides legacy", tier: ptrString(""), legacy: &truth},
		{name: "priority overrides legacy", tier: ptrString("priority"), legacy: &falsity, want: "priority"},
		{name: "ultrafast overrides legacy", tier: ptrString("ultrafast"), legacy: &truth, want: "ultrafast"},
		{name: "platform change clears", platform: PlatformAnthropic},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{getByID: &Group{
				ID: 1, Name: "existing", Platform: PlatformOpenAI, Status: StatusActive,
				ForceOpenAIServiceTier: "ultrafast",
			}}
			invalidator := &authCacheInvalidatorStub{}
			svc := &adminServiceImpl{groupRepo: repo, authCacheInvalidator: invalidator}
			group, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
				Platform: tt.platform, ForceOpenAIServiceTier: tt.tier, ForceOpenAIFast: tt.legacy,
			})
			require.NoError(t, err)
			require.Equal(t, tt.want, repo.updated.ForceOpenAIServiceTier)
			require.Equal(t, tt.want == "priority", group.ForceOpenAIFast)
			require.Equal(t, []int64{1}, invalidator.groupIDs)
		})
	}
}

func TestAdminService_RejectsInvalidForcedServiceTierBeforePersisting(t *testing.T) {
	for _, invalid := range []string{"fast", "flex", "default", "ULTRAFAST"} {
		t.Run(invalid, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{getByID: &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive}}
			svc := &adminServiceImpl{groupRepo: repo}
			_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
				Name: "invalid", Platform: PlatformOpenAI, RateMultiplier: 1, ForceOpenAIServiceTier: &invalid,
			})
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.Nil(t, repo.created)
			_, err = svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{ForceOpenAIServiceTier: &invalid})
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.Nil(t, repo.updated)
		})
	}
}

func TestCloneGroupForDuplicatePreservesUltrafast(t *testing.T) {
	source := &Group{Name: "ultrafast", Platform: PlatformOpenAI, ForceOpenAIServiceTier: "ultrafast"}
	duplicate := cloneGroupForDuplicate(source, "operation")
	require.Equal(t, "ultrafast", duplicate.ForceOpenAIServiceTier)
	require.False(t, duplicate.ForceOpenAIFast)
}
