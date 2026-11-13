package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Only the proxy ID and public address are stored with the moderation settings.
// Proxy credentials stay in the proxy repository. Hashes are scoped by engine.
type ContentModerationKeyIPv6 struct {
	ProxyID int64  `json:"proxy_id"`
	Address string `json:"address"`
}

var ErrContentModerationConfigConflict = infraerrors.Conflict("CONTENT_MODERATION_CONFIG_CONFLICT", "审计配置已变化，请刷新后重试")

type contentModerationConfigWriter interface {
	UpdateContentModerationConfigAtomic(context.Context, string, []int64, func(context.Context) (string, error)) error
}

type contentModerationIPv6Allocator interface {
	AllocateContentModerationIPv6(context.Context, string) (ContentModerationKeyIPv6, error)
	RetireContentModerationIPv6(context.Context, string, int64) error
}

func cloneModerationKeyIPv6(bindings map[string]ContentModerationKeyIPv6) map[string]ContentModerationKeyIPv6 {
	out := make(map[string]ContentModerationKeyIPv6, len(bindings))
	for hash, binding := range bindings {
		out[hash] = binding
	}
	return out
}

func attachModerationKeyIPv6(status *ContentModerationAPIKeyStatus, cfg *ContentModerationConfig) {
	if binding, ok := cfg.KeyIPv6[status.KeyHash]; ok {
		status.ProxyID = &binding.ProxyID
		status.ManagedIPv6 = binding.Address
	}
}

func (s *ContentModerationService) apiKeyStatusesWithIPv6(cfg *ContentModerationConfig, keys []string) []ContentModerationAPIKeyStatus {
	statuses := s.apiKeyStatuses(keys, cfg.Engine)
	for i := range statuses {
		attachModerationKeyIPv6(&statuses[i], cfg)
	}
	return statuses
}

// Allocation is idempotent for a saved key. A stale concurrent configuration
// cannot overwrite another key's binding; retry reloads the committed result.
func (s *ContentModerationService) AllocateAPIKeyIPv6(ctx context.Context, engine, hash string) (*ContentModerationConfigView, error) {
	if !validModerationEngine(engine) || len(hash) != 64 {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_KEY", "请选择已保存的审计 Key")
	}
	for attempt := 0; attempt < 3; attempt++ {
		view, err := s.updateConfig(ctx, UpdateContentModerationConfigInput{}, map[string][]string{engine: {hash}})
		if !errors.Is(err, ErrContentModerationConfigConflict) {
			return view, err
		}
	}
	return nil, ErrContentModerationConfigConflict
}

func (s *ContentModerationService) saveConfigWithIPv6(ctx context.Context, previous, cfg *ContentModerationConfig, previousRaw string, requested map[string][]string) (*ContentModerationConfigView, error) {
	type allocation struct{ engine, hash string }
	type retirement struct {
		hash string
		id   int64
	}
	var allocations []allocation
	var retirements []retirement
	profiles := make(map[string]*ContentModerationEngineConfig)
	for _, engine := range []string{ContentModerationEngineOpenAI, ContentModerationEngineTypeSafe} {
		old, profile := previous.engineProfile(engine), cfg.engineProfile(engine)
		keys, oldKeys := map[string]bool{}, map[string]bool{}
		for _, key := range old.APIKeys {
			oldKeys[scopedModerationKeyHash(key, engine)] = true
		}
		for _, key := range profile.APIKeys {
			keys[scopedModerationKeyHash(key, engine)] = true
		}
		want := map[string]bool{}
		for _, hash := range requested[engine] {
			if !keys[hash] {
				return nil, infraerrors.BadRequest("CONTENT_MODERATION_KEY_NOT_FOUND", "审计 Key 已被删除，请刷新后重试")
			}
			want[hash] = true
		}
		profile.KeyIPv6 = cloneModerationKeyIPv6(old.KeyIPv6)
		for hash, binding := range old.KeyIPv6 {
			if !keys[hash] {
				retirements = append(retirements, retirement{hash, binding.ProxyID})
				delete(profile.KeyIPv6, hash)
			}
		}
		for _, key := range profile.APIKeys {
			hash := scopedModerationKeyHash(key, engine)
			if _, exists := profile.KeyIPv6[hash]; exists {
				continue
			}
			if want[hash] || (profile.AutoAssignIPv6 && !oldKeys[hash]) {
				allocations = append(allocations, allocation{engine, hash})
			}
		}
		profiles[engine] = profile
	}
	if len(allocations) > 100 {
		return nil, infraerrors.BadRequest("CONTENT_MODERATION_IPV6_BATCH_TOO_LARGE", "每次最多为 100 个新增审计 Key 分配 IPv6")
	}
	allocator, canAllocate := s.proxyRepo.(contentModerationIPv6Allocator)
	writer, atomic := s.settingRepo.(contentModerationConfigWriter)
	if (len(allocations) > 0 || len(retirements) > 0) && (!canAllocate || !atomic) {
		return nil, infraerrors.ServiceUnavailable("CONTENT_MODERATION_IPV6_UNAVAILABLE", "审计 Key 的 OVH IPv6 分配服务不可用")
	}
	var raw []byte
	write := func(txctx context.Context) (string, error) {
		for _, allocation := range allocations {
			binding, err := allocator.AllocateContentModerationIPv6(txctx, allocation.hash)
			if err != nil {
				return "", fmt.Errorf("分配审计 Key IPv6: %w", err)
			}
			profiles[allocation.engine].KeyIPv6[allocation.hash] = binding
		}
		for _, retired := range retirements {
			if err := allocator.RetireContentModerationIPv6(txctx, retired.hash, retired.id); err != nil {
				return "", err
			}
		}
		cfg.applyEngineProfile(profiles[ContentModerationEngineOpenAI])
		if cfg.TypeSafe != nil || len(profiles[ContentModerationEngineTypeSafe].KeyIPv6) > 0 {
			cfg.TypeSafe = profiles[ContentModerationEngineTypeSafe]
		}
		var err error
		raw, err = json.Marshal(cfg)
		return string(raw), err
	}
	if atomic {
		if err := writer.UpdateContentModerationConfigAtomic(ctx, previousRaw, cfg.GroupIDs, write); err != nil {
			if errors.Is(err, ErrGroupNotFound) {
				return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_GROUP", "审计分组不存在")
			}
			return nil, err
		}
	} else {
		if _, err := write(ctx); err != nil {
			return nil, err
		}
		var err error
		raw, err = s.saveConfigLegacy(ctx, cfg)
		if err != nil {
			return nil, err
		}
	}
	s.replaceRuntimeConfig(cfg, raw)
	s.moderationProxyCache.Store(nil)
	return s.engineConfigView(cfg), nil
}

func (s *ContentModerationService) rejectSharedModerationIPv6(ctx context.Context, id int64, resolved ...*Proxy) error {
	var proxy *Proxy
	if len(resolved) > 0 {
		proxy = resolved[0]
	} else if s.proxyRepo != nil {
		var err error
		proxy, err = s.proxyRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}
	}
	// Legacy v6-sub2 leases are owned by their account family too. They must
	// never become a shared moderation route, even without an OVH lease row.
	if proxy.IsManagedIPv6() {
		return infraerrors.BadRequest("CONTENT_MODERATION_IPV6_EXCLUSIVE", "固定 IPv6 不能用作共享审计代理，请为每个审计 Key 单独分配")
	}
	if repo, ok := s.proxyRepo.(interface {
		ManagedIPv6Details(context.Context, []int64) (map[int64]ManagedIPv6Binding, error)
	}); ok {
		bindings, err := repo.ManagedIPv6Details(ctx, []int64{id})
		if err != nil {
			return err
		}
		if _, managed := bindings[id]; managed {
			return infraerrors.BadRequest("CONTENT_MODERATION_IPV6_EXCLUSIVE", "OVH IPv6 必须为每个审计 Key 单独分配，不能用作共享代理")
		}
	}
	return nil
}
