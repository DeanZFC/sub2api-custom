package service

import (
	"context"
	"net/http"
)

// The model belongs to the outgoing response.create payload. Keep this factory
// independent of Agent Identity's per-dial authorization factory: selecting an
// existing socket must also check ticket freshness without minting credentials.
func (s *OpenAIGatewayService) openAIWSCodexTicketHeadersFactory(account *Account, model string) func(context.Context, http.Header) (http.Header, bool, error) {
	return func(ctx context.Context, headers http.Header) (http.Header, bool, error) {
		policy := s.openAICodexTicketAccountConfig(ctx, account)
		if !policy.Enabled || !s.openAICodexTicketGatedModel(model) {
			return headers, false, nil
		}
		if headers == nil {
			headers = make(http.Header)
		}
		managed, err := s.applyOpenAICodexTicketWithResult(ctx, account, model, headers)
		if err != nil {
			return nil, false, err
		}
		return headers, managed, nil
	}
}
