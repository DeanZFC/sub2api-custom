package service

import (
	"context"
	"fmt"
	"maps"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ConfigureManagedIPv6Concurrency is called during handler construction, before
// requests are accepted. Ordinary account edits do not acquire this dependency.
func ConfigureManagedIPv6Concurrency(admin AdminService, concurrency *ConcurrencyService) {
	if s, ok := admin.(*adminServiceImpl); ok {
		s.managedIPv6Concurrency = concurrency
	}
}

func (p *Proxy) IsOVHIPv6() bool { return p != nil && strings.HasPrefix(p.Username, "ovh6_") }

func (p *Proxy) IsManagedIPv6() bool {
	return p != nil && (p.IsOVHIPv6() || strings.HasPrefix(p.Username, "v6-sub2-"))
}

func (a *Account) IsOVHIPv6Bound() bool {
	if a == nil {
		return false
	}
	marked, _ := a.Extra["ovh_ipv6_bound"].(bool)
	return marked || a.Proxy.IsOVHIPv6()
}

func (a *Account) IsManagedIPv6Bound() bool {
	return a != nil && (a.IsOpenAIIPv6Bound() || a.IsOVHIPv6Bound())
}

func (s *adminServiceImpl) requiresManagedProxyTransaction(ctx context.Context, id *int64) bool {
	if s.proxyRepo == nil || id == nil || *id <= 0 || s.entClient == nil || dbent.TxFromContext(ctx) != nil {
		return false
	}
	p, err := s.proxyRepo.GetByID(ctx, *id)
	return err == nil && p.IsOVHIPv6()
}

func (s *adminServiceImpl) createAccountProxyMode(ctx context.Context, input *CreateAccountInput) (*Account, error) {
	if len(input.ProxyIDs) > 0 {
		return nil, infraerrors.BadRequest("PROXY_SELECTION_CONFLICT", "自动代理分配不能同时指定代理池")
	}
	if _, err := NormalizeAccountProxyMode(input.ProxyMode, input.ProxyID, false); err != nil {
		return nil, err
	}
	if input.ProxyMode == "ovh_ipv6" || s.managedProxyID(ctx, input.ProxyID) {
		if err := validateOVHIPv6Eligibility(&Account{Platform: input.Platform, Type: input.Type, Credentials: input.Credentials, Extra: input.Extra}); err != nil {
			return nil, err
		}
	}
	if dbent.TxFromContext(ctx) == nil {
		return s.scopedAdminCreateTransaction(ctx, input)
	}
	safe := *input
	id, err := s.scopedAccountProxySelection(ctx, safe.ProxyMode, safe.ProxyID)
	if err != nil {
		return nil, err
	}
	safe.ProxyID, safe.ProxyMode = id, ""
	if id != nil && *id == 0 {
		safe.ProxyID = nil
	}
	return s.CreateAccount(ctx, &safe)
}

func (s *adminServiceImpl) updateAccountProxyMode(ctx context.Context, id int64, input *UpdateAccountInput) (*Account, error) {
	if input.ProxyIDs != nil && len(*input.ProxyIDs) > 0 {
		return nil, infraerrors.BadRequest("PROXY_SELECTION_CONFLICT", "自动代理分配不能同时指定代理池")
	}
	if _, err := NormalizeAccountProxyMode(input.ProxyMode, input.ProxyID, false); err != nil {
		return nil, err
	}
	if input.ProxyMode == "" && input.ProxyIDs != nil && len(*input.ProxyIDs) == 0 {
		zero := int64(0)
		input.ProxyID = &zero
	}
	run := func(txctx context.Context) (*Account, error) {
		a, err := s.accountRepo.GetByID(txctx, id)
		if err != nil {
			return nil, err
		}
		if a.IsCredentialShadow() {
			return nil, infraerrors.BadRequest("SPARK_SHADOW_PROXY_INHERITED", "请在母账号上更改固定 IPv6")
		}
		if a.IsOpenAIIPv6Bound() {
			return nil, infraerrors.Conflict("LEGACY_IPV6_BOUND", "请先通过固定 IPv6 出口入口解绑并恢复原路由")
		}
		changesRoute := input.ProxyMode == "ovh_ipv6" || input.ProxyMode == "random" || input.ProxyMode == "least_used" || (input.ProxyMode == "direct" && a.ProxyID != nil) || (input.ProxyID != nil && (a.ProxyID == nil || *a.ProxyID != *input.ProxyID))
		if changesRoute && (a.IsOVHIPv6Bound() || input.ProxyMode == "ovh_ipv6" || s.managedProxyID(txctx, input.ProxyID)) {
			if input.ProxyMode == "ovh_ipv6" || s.managedProxyID(txctx, input.ProxyID) {
				if err = validateOVHIPv6Eligibility(a); err != nil {
					return nil, err
				}
			}
			if err = s.managedIPv6Drain(txctx, a); err != nil {
				return nil, err
			}
		}
		safe := *input
		safe.ProxyID, err = s.scopedAccountProxySelection(txctx, safe.ProxyMode, safe.ProxyID)
		if err != nil {
			return nil, err
		}
		safe.ProxyMode, safe.ProxyIDs = "", nil
		return s.UpdateAccount(txctx, id, &safe)
	}
	if dbent.TxFromContext(ctx) != nil {
		return run(ctx)
	}
	return s.scopedAdminTransaction(ctx, id, false, run)
}

func (s *adminServiceImpl) managedIPv6Drain(ctx context.Context, account *Account) error {
	if account == nil {
		return fmt.Errorf("account unavailable")
	}
	shadows, err := s.accountRepo.ListShadowsByParent(ctx, account.ID)
	if err != nil {
		return err
	}
	family := append(shadows, account)
	ids := make([]int64, 0, len(family))
	for _, a := range family {
		if a.Status != StatusDisabled && a.Status != "inactive" {
			return infraerrors.Conflict("MANAGED_IPV6_DRAIN_REQUIRED", "请先停用母账号及全部影子账号，并等待在途请求完成")
		}
		ids = append(ids, a.ID)
	}
	if s.managedIPv6Concurrency == nil || s.managedIPv6Concurrency.cache == nil {
		return infraerrors.Conflict("MANAGED_IPV6_DRAIN_UNAVAILABLE", "无法核对在途请求，暂不能更改固定 IPv6")
	}
	counts, err := s.managedIPv6Concurrency.GetAccountConcurrencyBatch(ctx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if n, ok := counts[id]; !ok || n != 0 {
			return infraerrors.Conflict("MANAGED_IPV6_DRAIN_REQUIRED", "仍有在途请求，请等待完成后重试")
		}
	}
	return nil
}

func (s *adminServiceImpl) validateManagedIPv6AccountChange(ctx context.Context, account *Account, input *UpdateAccountInput) error {
	currentManaged := account.IsOVHIPv6Bound()
	var next *Proxy
	if s.proxyRepo != nil && input.ProxyID != nil && *input.ProxyID > 0 {
		var err error
		next, err = s.proxyRepo.GetByID(ctx, *input.ProxyID)
		if err != nil {
			return err
		}
	}
	if !currentManaged && !next.IsOVHIPv6() {
		return nil
	}
	merged := *account
	merged.Extra = maps.Clone(account.Extra)
	if merged.Extra == nil {
		merged.Extra = map[string]any{}
	}
	maps.Copy(merged.Extra, input.Extra)
	if input.Credentials != nil {
		merged.Credentials = input.Credentials
	}
	if input.Type != "" {
		merged.Type = input.Type
	}
	if err := validateOVHIPv6Eligibility(&merged); err != nil {
		return err
	}
	if input.ProxyIDs != nil && len(*input.ProxyIDs) > 1 {
		return infraerrors.BadRequest("MANAGED_IPV6_POOL", "固定 IPv6 不能加入轮转代理池")
	}
	changesRoute := input.ProxyID != nil && (account.ProxyID == nil || *input.ProxyID != *account.ProxyID)
	if changesRoute {
		if input.Status == StatusActive {
			return infraerrors.Conflict("MANAGED_IPV6_DRAIN_REQUIRED", "更改出口与启用账号须分开操作")
		}
		if err := s.managedIPv6Drain(ctx, account); err != nil {
			return err
		}
	}
	return nil
}

func (s *adminServiceImpl) managedProxyID(ctx context.Context, id *int64) bool {
	if s.proxyRepo == nil || id == nil || *id <= 0 {
		return false
	}
	p, err := s.proxyRepo.GetByID(ctx, *id)
	return err == nil && p.IsOVHIPv6()
}

// Preserve the existing bulk patch semantics, but choose and bind each managed
// lease within the same transaction as that account's other changes.
func (s *adminServiceImpl) bulkUpdateManagedProxyAccounts(ctx context.Context, input *BulkUpdateAccountsInput) (*BulkUpdateAccountsResult, error) {
	if input.ProxyIDs != nil && len(*input.ProxyIDs) > 0 {
		return nil, infraerrors.BadRequest("PROXY_SELECTION_CONFLICT", "自动分配不能同时指定代理池")
	}
	if _, err := NormalizeAccountProxyMode(input.ProxyMode, input.ProxyID, false); err != nil {
		return nil, err
	}
	ids := input.AccountIDs
	if len(ids) == 0 && input.Filters != nil {
		var err error
		ids, err = s.resolveBulkUpdateTargetIDs(ctx, input.Filters)
		if err != nil {
			return nil, err
		}
	}
	result := &BulkUpdateAccountsResult{SuccessIDs: []int64{}, FailedIDs: []int64{}, Results: []BulkUpdateAccountResult{}}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		entry := BulkUpdateAccountResult{AccountID: id}
		run := func(txctx context.Context) (*Account, error) {
			route := &UpdateAccountInput{ProxyMode: input.ProxyMode, ProxyID: input.ProxyID, Status: input.Status}
			a, err := s.updateAccountProxyMode(txctx, id, route)
			if err != nil {
				return nil, err
			}
			patch := *input
			patch.AccountIDs = []int64{id}
			patch.Filters = nil
			patch.ProxyMode = ""
			patch.ProxyID = nil
			patch.ProxyIDs = nil
			patch.Extra = maps.Clone(input.Extra)
			patch.Credentials = maps.Clone(input.Credentials)
			updated, err := s.BulkUpdateAccounts(txctx, &patch)
			if err != nil {
				return nil, err
			}
			if updated.Failed > 0 {
				return nil, fmt.Errorf("%s", updated.Results[0].Error)
			}
			return a, nil
		}
		var a *Account
		err := ctx.Err()
		if err == nil {
			a, err = s.scopedAdminTransaction(ctx, id, false, run)
		}
		if err != nil {
			entry.Error = err.Error()
			result.Failed++
			result.FailedIDs = append(result.FailedIDs, id)
		} else {
			entry.Success = true
			entry.ProxyID = a.ProxyID
			result.Success++
			result.SuccessIDs = append(result.SuccessIDs, id)
		}
		result.Results = append(result.Results, entry)
	}
	return result, nil
}

func validateOVHIPv6Eligibility(a *Account) error {
	if a == nil || !a.IsOpenAIOAuth() || a.IsOpenAIAgentIdentity() || a.IsExcelOAuth() || a.Extra["openai_excel_bps"] == true || a.IsRetiredVMRoute() || a.IsOpenAIIPv6Bound() {
		return infraerrors.BadRequest("OVH_IPV6_INELIGIBLE", "OVH 固定 IPv6 仅支持原生 OpenAI OAuth，不能与 Excel BPS、Agent Identity、旧固定 IPv6 或 VM 路由叠加")
	}
	return nil
}
