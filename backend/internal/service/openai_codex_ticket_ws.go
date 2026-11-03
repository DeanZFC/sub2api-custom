package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
)

// A harvested ticket fixes the identity and exit of the WebSocket handshake.
// Subsequent turns may keep that binding while it remains valid, but must not
// silently replace it with a ticket minted for a different identity or exit.
type openAICodexWSBinding struct {
	model     string
	ticket    *openAICodexTicket
	headers   http.Header
	createdAt time.Time
}

func openAIWSRejectExcelBPS(account *Account, requestedModel, upstreamModel string) error {
	if account.IsExcelBPSEnabledForModel(requestedModel) || account.isExcelBPSUpstreamModelEnabled(upstreamModel) {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "Excel/BPS models require HTTP/SSE", denyOpenAITurn("excel_bps_requires_http"))
	}
	return nil
}

func (s *OpenAIGatewayService) bindCodexTicketWS(ctx context.Context, account *Account, model string, headers http.Header) *openAICodexWSBinding {
	binding := &openAICodexWSBinding{model: strings.TrimSpace(model), headers: headers.Clone(), createdAt: time.Now()}
	if ticket := s.boundCodexTicketFromHeader(ctx, headers, account); ticket != nil {
		copy := *ticket
		copy.Standby = nil
		binding.ticket = &copy
	}
	return binding
}

// checkCodexTicketWS refreshes account eligibility without refreshing OAuth
// credentials on an already authenticated socket. It accepts a still valid
// primary or standby ticket, but never switches tickets on an existing socket.
func (s *OpenAIGatewayService) checkCodexTicketWS(ctx context.Context, account *Account, model string, binding *openAICodexWSBinding) error {
	if err := openAIWSRejectExcelBPS(account, "", model); err != nil {
		return err
	}
	var authorization []string
	if binding != nil {
		authorization = []string{binding.headers.Get("Authorization")}
	}
	fresh, err := s.refreshCodexTicketAccountForSend(ctx, account, model, authorization...)
	if err != nil {
		return err
	}
	if err := openAIWSRejectExcelBPS(fresh, "", model); err != nil {
		return err
	}
	if !isOpenAICodexTicketAccount(fresh, model) || !s.openAICodexTicketEnabledContext(ctx) {
		return nil
	}
	if binding == nil || time.Since(binding.createdAt) >= openAIWSConnMaxAge {
		return denyOpenAITurn("connection_binding_expired")
	}
	if binding.ticket != nil {
		cfg := s.openAICodexTicketConfig()
		if normalizeOpenAICodexTicketModel(model) != binding.ticket.Model ||
			!binding.ticket.valid(time.Now(), openAICodexTicketTargetLength(fresh, cfg)) ||
			!ticketIdentityMatches(fresh, binding.ticket) {
			return denyOpenAITurn("connection_ticket_expired")
		}
		current := s.lookupOpenAICodexTicket(fresh, model)
		if current == nil || current.Revoked ||
			(current.State != binding.ticket.State && (current.Standby == nil || current.Standby.Revoked || current.Standby.State != binding.ticket.State)) {
			return denyOpenAITurn("connection_ticket_revoked")
		}
		return nil
	}
	// Fail-open sockets with no ticket may continue only while the active
	// policy would keep their original handshake. A newly required/injected
	// ticket needs a fresh socket and a newly pinned exit.
	candidate := binding.headers.Clone()
	if err := s.applyOpenAICodexTicket(ctx, fresh, model, candidate, "websocket"); err != nil {
		return err
	}
	if candidate.Get(openAICodexTurnStateHeader) != binding.headers.Get(openAICodexTurnStateHeader) {
		return denyOpenAITurn("connection_ticket_changed")
	}
	return nil
}

func (binding *openAICodexWSBinding) pinPayload(raw []byte) ([]byte, error) {
	if binding == nil || binding.ticket == nil {
		return raw, nil
	}
	next, _, err := pinBoundCodexTicketHarvestIdentityBody(raw, binding.ticket)
	return next, err
}

func (binding *openAICodexWSBinding) restoreHeaders(headers http.Header) {
	if binding == nil || binding.ticket == nil || headers == nil {
		return
	}
	headers.Set(openAICodexTurnStateHeader, binding.ticket.State)
	restoreBoundCodexTicketHarvestIdentity(headers, binding.ticket)
}
