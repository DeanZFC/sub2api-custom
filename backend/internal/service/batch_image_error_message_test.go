//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBatchImagePublicProviderErrorsUseConfiguredMessage(t *testing.T) {
	svc, repo, _, _, _ := newTestBatchImagePublicService(true)
	apiKeyID := int64(22)
	repo.jobs["batch_error_policy"] = &BatchImageJob{
		BatchID: "batch_error_policy", UserID: 11, APIKeyID: &apiKeyID,
		Provider: BatchImageProviderGeminiAPI, Status: BatchImageJobStatusCompleted, CreatedAt: time.Now(),
	}
	providerCode, providerMessage := "SAFETY_BLOCKED", "private provider diagnostic"
	systemCode, systemMessage := "INDEX_PARSE_FAILED", "local indexing failed"
	repo.items["batch_error_policy"] = []CreateBatchImageItemParams{
		{JobID: "batch_error_policy", CustomID: "provider", Status: BatchImageItemStatusFailed, ErrorCode: &providerCode, ErrorMessage: &providerMessage},
		{JobID: "batch_error_policy", CustomID: "local", Status: BatchImageItemStatusFailed, ErrorCode: &systemCode, ErrorMessage: &systemMessage},
	}
	ctx := WithUpstreamErrorMessage(context.Background(), "统一提示")
	items, err := svc.ListItems(ctx, testBatchImageOwner(), "batch_error_policy", BatchImageItemsQuery{})
	require.NoError(t, err)
	require.Len(t, items.Data, 2)
	require.Equal(t, "统一提示", items.Data[0].Error.Message)
	require.Equal(t, "local indexing failed", items.Data[1].Error.Message)
	require.Equal(t, "private provider diagnostic", *repo.items["batch_error_policy"][0].ErrorMessage)
}

func TestBatchImageZipProviderErrorsUseConfiguredMessage(t *testing.T) {
	svc, repo, _ := newTestBatchImageDownloadService()
	private := "private provider diagnostic"
	repo.items["imgbatch_download"][1].ErrorMessage = &private
	ctx := WithUpstreamErrorMessage(context.Background(), "统一提示")
	var buffer bytes.Buffer
	_, err := svc.StreamZip(ctx, testBatchImageOwner(), "imgbatch_download", BatchImageZipOptions{}, &buffer)
	require.NoError(t, err)
	files := readZipFiles(t, buffer.Bytes())
	var failures []batchImageZipError
	require.NoError(t, json.Unmarshal(files["errors.json"], &failures))
	require.Len(t, failures, 1)
	require.Equal(t, "统一提示", failures[0].Message)
	require.NotContains(t, string(files["errors.json"]), "private")
	require.Equal(t, "private provider diagnostic", *repo.items["imgbatch_download"][1].ErrorMessage)
	// Successful image bytes remain untouched.
	require.Equal(t, []byte("first"), files["images/cover___001.png"])
}
