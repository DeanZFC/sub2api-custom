package service

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// Recheck the persisted account at the send boundary. A previously selected
// account must not carry a ticket to a disabled account or a changed identity,
// protocol or proxy. Ticket refreshes themselves do not invalidate a request.
func (s *OpenAIGatewayService) refreshCodexTicketAccountForSend(ctx context.Context, account *Account, model string, authorization ...string) (*Account, error) {
	if s == nil || !s.isOpenAICodexTicketAccount(ctx, account, model) || !s.openAICodexTicketEnabledContext(ctx) || !s.openAICodexTicketGatedModel(model) {
		return account, nil
	}
	if s.accountRepo == nil {
		return nil, denyOpenAITurn("account_state_unavailable")
	}
	fresh, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil || fresh == nil {
		return nil, denyOpenAITurn("account_state_unavailable")
	}
	if !fresh.IsSchedulableForModelWithContext(ctx, model) || s.isOpenAIAccountRequestRuntimeBlocked(fresh, model) || !s.isOpenAICodexTicketAccount(ctx, fresh, model) {
		return nil, denyOpenAITurn("account_no_longer_eligible")
	}
	if fresh.ID != account.ID || fresh.Platform != account.Platform || fresh.Type != account.Type ||
		ticketIdentity(fresh) != ticketIdentity(account) || !codexTicketSendCredentialsMatch(account, fresh, authorization) ||
		!slices.Equal(fresh.GroupIDs, account.GroupIDs) || !slices.Equal(fresh.ProxyIDs, account.ProxyIDs) ||
		fresh.IsOpenAIVMBound() != account.IsOpenAIVMBound() {
		return nil, denyOpenAITurn("account_route_changed")
	}
	// A pool reservation chooses a request-local ProxyID; retain that same
	// reservation only if the current account still owns an active route.
	if account.SelectedProxyID != 0 {
		fresh, err = fresh.WithProxyRoute(account.SelectedProxyID)
		if err != nil {
			return nil, denyOpenAITurn("account_route_changed")
		}
	}
	if !reflect.DeepEqual(fresh.ProxyID, account.ProxyID) || openAIAccountProxyURL(fresh) != openAIAccountProxyURL(account) {
		return nil, denyOpenAITurn("account_route_changed")
	}
	return fresh, nil
}

func codexTicketSendCredentialsMatch(selected, fresh *Account, authorization []string) bool {
	if reflect.DeepEqual(selected.Credentials, fresh.Credentials) {
		return true
	}
	if len(authorization) == 0 {
		return false
	}
	scheme, token, ok := strings.Cut(strings.TrimSpace(authorization[0]), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || token != fresh.GetCredential("access_token") {
		return false
	}
	// The token provider may have refreshed credentials while the forwarding
	// caller still holds its earlier snapshot. Permit that refresh only when
	// the outgoing Authorization already uses the persisted current token and
	// every identity/routing credential is unchanged. Administrative replacement
	// after request construction still fails this check.
	stable := func(credentials map[string]any) map[string]any {
		out := maps.Clone(credentials)
		for _, key := range []string{"access_token", "refresh_token", "id_token", "expires_at", "_token_version", "plan_type", "subscription_expires_at"} {
			delete(out, key)
		}
		return out
	}
	return reflect.DeepEqual(stable(selected.Credentials), stable(fresh.Credentials))
}
