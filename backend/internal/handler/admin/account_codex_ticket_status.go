package admin

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"time"
)

func (h *AccountHandler) enrichCodexTicketStatus(account *service.Account, out *dto.Account) {
	if h == nil || h.cfg == nil || account == nil || out == nil {
		return
	}
	cfg := h.cfg.Gateway.OpenAICodexTicket
	if h.codexTicketSettings != nil {
		cfg.Enabled = h.codexTicketSettings.GetOpenAICodexTicketEnabled(context.Background(), cfg.Enabled)
		cfg.Models = h.codexTicketSettings.GetOpenAICodexTicketModels(context.Background(), cfg.Models)
		cfg.FailClosed = h.codexTicketSettings.GetOpenAICodexTicketFailClosed(context.Background())
	}
	out.CodexTurnTickets = service.OpenAICodexTicketStatuses(account, cfg, time.Now())
}
