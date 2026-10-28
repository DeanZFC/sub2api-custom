//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestDisplayEvidenceQualityScoresDeduplicateAndBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	config := service.DefaultChannelQualityScoreConfig()
	config.Enabled = false
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	// 101 accounts for one model require two bounded batches; repeated card
	// rows share their score lookup. A different model remains independent.
	var results []*service.ScheduledTestResult
	for i := int64(1); i <= 101; i++ {
		id := i
		results = append(results, &service.ScheduledTestResult{AccountID: &id, ModelID: "model-a"})
	}
	first := int64(1)
	results = append(results, &service.ScheduledTestResult{AccountID: &first, ModelID: "model-a"}, &service.ScheduledTestResult{AccountID: &first, ModelID: "model-b"}, &service.ScheduledTestResult{ModelID: "model-a"}, nil)
	for i := 0; i < 3; i++ {
		mock.ExpectQuery(`SELECT config,version,updated_at,collect_since FROM channel_quality_score_config`).
			WillReturnRows(sqlmock.NewRows([]string{"config", "version", "updated_at", "collect_since"}).AddRow(raw, 1, time.Now(), time.Now()))
		mock.ExpectBegin()
		mock.ExpectCommit()
	}
	repo.hydrateQualityScores(context.Background(), results)
	for i := 0; i < 103; i++ {
		require.NotNil(t, results[i].QualityScore, fmt.Sprintf("card %d", i))
		require.Equal(t, "disabled", results[i].QualityScore.Status)
		require.Equal(t, results[i].ModelID, results[i].QualityScore.Model)
		require.Nil(t, results[i].QualityScore.Value)
	}
	require.Nil(t, results[103].QualityScore, "account-free cards cannot expose account score evidence")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestDisplayEvidenceOptionalReadsFailIndependently(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	id := int64(62)
	result := &service.ScheduledTestResult{ID: 9, AccountID: &id, ModelID: "model-a", Status: "success"}
	mock.ExpectQuery(`SELECT r.id,s.plan_id.*scheduled_test_pass_rate_states`).WithArgs("{9}").WillReturnError(errors.New("pass-rate unavailable"))
	mock.ExpectQuery(`SELECT config,version,updated_at,collect_since FROM channel_quality_score_config`).WillReturnError(errors.New("score unavailable"))
	repo.hydrateDisplayEvidence(context.Background(), []*service.ScheduledTestResult{result})
	require.Equal(t, "success", result.Status)
	require.Nil(t, result.PassRate)
	require.Nil(t, result.QualityScore)
	require.NoError(t, mock.ExpectationsWereMet())
}
