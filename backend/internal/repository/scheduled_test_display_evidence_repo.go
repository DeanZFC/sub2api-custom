package repository

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Display evidence is optional and never changes governance. A failed evidence
// read must not hide the already-authorized test results themselves.
func (r *scheduledTestResultRepository) hydrateDisplayEvidence(ctx context.Context, results []*service.ScheduledTestResult) {
	_ = r.hydratePassRates(ctx, results)
	r.hydrateQualityScores(ctx, results)
}

func (r *scheduledTestResultRepository) hydrateQualityScores(ctx context.Context, results []*service.ScheduledTestResult) {
	byModel := map[string]map[int64][]*service.ScheduledTestResult{}
	for _, result := range results {
		if result == nil || result.AccountID == nil || *result.AccountID <= 0 {
			continue
		}
		model := strings.TrimSpace(result.ModelID)
		if model == "" {
			continue
		}
		if byModel[model] == nil {
			byModel[model] = map[int64][]*service.ScheduledTestResult{}
		}
		byModel[model][*result.AccountID] = append(byModel[model][*result.AccountID], result)
	}
	models := make([]string, 0, len(byModel))
	for model := range byModel {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		accounts := byModel[model]
		ids := make([]int64, 0, len(accounts))
		for id := range accounts {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		// Bound each real-traffic read, independently of the number of test cards.
		const batchSize = 100
		for start := 0; start < len(ids); start += batchSize {
			batch := ids[start:min(start+batchSize, len(ids))]
			scores, err := queryChannelQualityScores(ctx, r.db, batch, model)
			if err != nil {
				continue
			}
			for _, id := range batch {
				for _, result := range accounts[id] {
					result.QualityScore = scores[id]
				}
			}
		}
	}
}
