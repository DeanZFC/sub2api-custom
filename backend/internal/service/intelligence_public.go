package service

import (
	"context"
	"errors"
	"time"
)

// Public intelligence DTOs contain anonymous group-local aliases, never
// upstream tokens, raw probe responses, admin notes, or scheduler identities.
type PublicIntelligenceGroup struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	Models           []string `json:"models"`
	HasLegacyResults bool     `json:"has_legacy_results"`
}
type PublicIntelligenceDirectory struct {
	RoundsVisible bool                      `json:"rounds_visible"`
	Groups        []PublicIntelligenceGroup `json:"groups"`
}

type PublicIntelligenceCounts struct {
	Pass     int `json:"pass"`
	Suspect  int `json:"suspect"`
	Degraded int `json:"degraded"`
	Unknown  int `json:"unknown"`
}
type PublicIntelligenceModelCounts struct {
	Model    string `json:"model"`
	Pass     int    `json:"pass"`
	Degraded int    `json:"degraded"`
}
type PublicIntelligenceRound struct {
	ID            string                          `json:"id"`
	StartedAt     time.Time                       `json:"started_at"`
	FinishedAt    time.Time                       `json:"finished_at"`
	Verdict       string                          `json:"verdict"`
	Counts        PublicIntelligenceCounts        `json:"counts"`
	ModelCounts   []PublicIntelligenceModelCounts `json:"model_counts"`
	Accounts      int                             `json:"accounts"`
	Targets       int                             `json:"targets"`
	PassedTargets int                             `json:"passed_targets"`
}
type PublicIntelligenceCheck struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type PublicIntelligenceModel struct {
	Model         string                    `json:"model"`
	Effort        string                    `json:"effort"`
	FinishedAt    *time.Time                `json:"finished_at"`
	Verdict       string                    `json:"verdict"`
	Checks        []PublicIntelligenceCheck `json:"checks"`
	ArtworkStatus string                    `json:"artwork_status"`
	ArtworkID     *int64                    `json:"artwork_id,omitempty"`
}
type PublicIntelligenceAccount struct {
	Alias         string                    `json:"alias"`
	Verdict       string                    `json:"verdict"`
	Targets       int                       `json:"targets"`
	PassedTargets int                       `json:"passed_targets"`
	Models        []PublicIntelligenceModel `json:"models"`
}

const PublicIntelligencePageSize = 25

type PublicIntelligenceRoundDetails struct {
	Round    PublicIntelligenceRound     `json:"round"`
	Accounts []PublicIntelligenceAccount `json:"accounts"`
	Total    int                         `json:"total"`
	Page     int                         `json:"page"`
	PageSize int                         `json:"page_size"`
}
type PublicIntelligenceModelStats struct {
	Model        string                   `json:"model"`
	Effort       string                   `json:"effort"`
	Counts       PublicIntelligenceCounts `json:"counts"`
	LastTestedAt *time.Time               `json:"last_tested_at"`
}
type PublicIntelligenceAccountStats struct {
	Alias          string                         `json:"alias"`
	Participations int                            `json:"participations"`
	Counts         PublicIntelligenceCounts       `json:"counts"`
	Models         []PublicIntelligenceModelStats `json:"models"`
}
type PublicIntelligenceSummary struct {
	Rounds         int                              `json:"rounds"`
	Accounts       int                              `json:"accounts"`
	Participations int                              `json:"participations"`
	Counts         PublicIntelligenceCounts         `json:"counts"`
	Targets        int                              `json:"targets"`
	PassedTargets  int                              `json:"passed_targets"`
	Items          []PublicIntelligenceAccountStats `json:"items"`
	Total          int                              `json:"total"`
	Page           int                              `json:"page"`
	PageSize       int                              `json:"page_size"`
}
type PublicIntelligenceRepository interface {
	PublicIntelligenceDirectory(context.Context, int64) (*PublicIntelligenceDirectory, error)
	PublicIntelligenceRounds(context.Context, int64, int64, string) ([]PublicIntelligenceRound, error)
	PublicIntelligenceRoundDetails(context.Context, int64, int64, string, string, int) (*PublicIntelligenceRoundDetails, error)
	PublicIntelligenceSummary(context.Context, int64, int64, string, int) (*PublicIntelligenceSummary, error)
	PublicIntelligenceArtwork(context.Context, int64, int64, int64) (string, error)
}

func (s *ScheduledTestService) PublicIntelligence() (PublicIntelligenceRepository, error) {
	if s != nil {
		if repo, ok := s.resultRepo.(PublicIntelligenceRepository); ok {
			return repo, nil
		}
	}
	return nil, errors.New("intelligence monitoring unavailable")
}
