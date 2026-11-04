package service

import (
	"context"
	"errors"
	"time"
)

type AstraGatewayHistoryRecord struct {
	Gateway         string     `json:"gateway"`
	SourceAccountID int64      `json:"source_account_id"`
	TargetAccountID int64      `json:"target_account_id"`
	Passes          int64      `json:"passes"`
	Failures        int64      `json:"failures"`
	FirstSeen       time.Time  `json:"first_seen"`
	LastSeen        time.Time  `json:"last_seen"`
	LastPass        *time.Time `json:"last_pass,omitempty"`
	LastFailure     *time.Time `json:"last_failure,omitempty"`
	LastAnswer      string     `json:"last_answer"`
	LastReason      string     `json:"last_reason"`
}
type AstraGatewayHistoryPage struct {
	Items          []AstraGatewayHistoryRecord `json:"items"`
	Total          int64                       `json:"total"`
	UniqueGateways int64                       `json:"unique_gateways"`
}
type AstraGatewayHistoryRepository interface {
	RecordAstraGateway(context.Context, AstraGatewayHistoryRecord, bool) error
	ListAstraGateways(context.Context, string, bool, int, int) (AstraGatewayHistoryPage, error)
}
type AstraGatewayHistoryRecorder interface {
	SetAstraGatewayHistoryRecorder(func(AstraGatewayHistoryRecord, bool))
}

func (s *AccountTestService) AstraGatewayHistory(ctx context.Context, host string, passed bool, offset, limit int) (AstraGatewayHistoryPage, error) {
	if s.settingService != nil {
		if repo, ok := s.settingService.settingRepo.(AstraGatewayHistoryRepository); ok {
			return repo.ListAstraGateways(ctx, host, passed, offset, limit)
		}
	}
	return AstraGatewayHistoryPage{}, errors.New("gateway_history_unavailable")
}

// Record only sanitized route metadata; tickets, cookies and proxy credentials
// remain in the private account bundle. Observation failures do not revoke tickets.
func (s *OpenAIGatewayService) recordStoredAstraHistory(ctx context.Context, ticket *openAICodexTicket, sourceID, targetID int64, passed bool, reason string) {
	if s == nil || s.settingService == nil || ticket == nil || ticket.Gateway == "" || ticket.Length != 780 || ticket.Transport != "sse" {
		return
	}
	repo, ok := s.settingService.settingRepo.(AstraGatewayHistoryRepository)
	if !ok {
		return
	}
	recordCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_ = repo.RecordAstraGateway(recordCtx, AstraGatewayHistoryRecord{Gateway: "chat.gateway." + ticket.Gateway + ".api.openai.com", SourceAccountID: sourceID, TargetAccountID: targetID, LastSeen: time.Now(), LastReason: reason}, passed)
}
