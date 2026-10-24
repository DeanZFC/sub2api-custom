package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsRepositoryListErrorLogsRetainsUpstreamEvidence(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &opsRepository{db: db}
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM ops_error_logs`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	rows := sqlmock.NewRows([]string{
		"id", "created_at", "error_phase", "error_type", "error_owner", "error_source", "severity", "status_code", "platform", "model",
		"resolved", "resolved_at", "resolved_by_user_id", "resolved_by_name", "client_request_id", "request_id", "error_message", "user_id", "email",
		"api_key_id", "account_id", "account_name", "group_id", "group_name", "client_ip", "request_path", "stream", "inbound_endpoint", "upstream_endpoint",
		"requested_model", "upstream_model", "user_agent", "request_type", "api_key_name", "api_key_deleted_at", "has_upstream_error",
	}).AddRow(1, time.Now(), "request", "invalid_request_error", "client", "client_request", "P3", 400, "gemini", "gemini-test",
		false, nil, nil, "", "", "req-1", "provider validation detail", 7, "", 9, 11, "", 3, "", nil, "/v1beta/models/test:generateContent", false,
		"", "", "", "", "", nil, "", nil, true)
	mock.ExpectQuery(`(?s)SELECT.*e\.upstream_status_code IS NOT NULL OR COALESCE\(e\.upstream_error_message, ''\) <> ''.*FROM ops_error_logs`).WillReturnRows(rows)
	result, err := repo.ListErrorLogs(context.Background(), &service.OpsErrorLogFilter{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	require.True(t, result.Errors[0].HasUpstreamError)
	require.Equal(t, "provider validation detail", result.Errors[0].Message)
	require.NoError(t, mock.ExpectationsWereMet())
}
