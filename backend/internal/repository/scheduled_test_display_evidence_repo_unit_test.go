//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestDisplayEvidencePassRateFailurePreservesResults(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	id := int64(62)
	result := &service.ScheduledTestResult{ID: 9, AccountID: &id, ModelID: "model-a", Status: "success"}
	mock.ExpectQuery(`SELECT r.id,s.plan_id.*scheduled_test_pass_rate_states`).WithArgs("{9}").WillReturnError(errors.New("pass-rate unavailable"))
	repo.hydrateDisplayEvidence(context.Background(), []*service.ScheduledTestResult{result})
	require.Equal(t, "success", result.Status)
	require.Nil(t, result.PassRate)
	require.NoError(t, mock.ExpectationsWereMet())
}
