package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// Retry only screenshot infrastructure, never generation or a paid judge call.
// Both attempts and the cancellable delay share the existing review deadline.
func (s *ScheduledTestRunnerService) renderAutomaticReview(ctx context.Context, planID int64, result *ScheduledTestResult, review *ScheduledTestAutoReviewSummary, retryDelay time.Duration) []ScheduledTestReviewFrame {
	started := time.Now()
	defer func() { review.RenderDurationMs = time.Since(started).Milliseconds() }()
	var failure *scheduledTestRenderError
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			failure = scheduledTestRenderTransportFailure(ctx.Err())
			break
		}
		review.RenderAttempts++
		frames, err := s.reviewRenderer.Render(ctx, result.OutputHTML)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err == nil && len(frames) == 4 {
			return frames
		}
		if err == nil {
			err = &scheduledTestRenderError{code: "renderer_invalid_response", diagnostic: "frame_count"}
		}
		failure = scheduledTestRenderFailure(err)
		accountID := int64(0)
		if result.AccountID != nil {
			accountID = *result.AccountID
		}
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestAutoReview] plan=%d result=%d account=%d stage=render attempt=%d code=%s diagnostic=%s http_status=%d retryable=%t", planID, result.ID, accountID, review.RenderAttempts, failure.code, failure.diagnostic, failure.httpStatus, failure.retryable())
		if !failure.retryable() || attempt == 1 {
			break
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			failure = scheduledTestRenderTransportFailure(ctx.Err())
		case <-timer.C:
		}
		if ctx.Err() != nil {
			break
		}
	}
	review.FailureStage, review.ErrorCode = "render", failure.code
	review.RenderRetryable = failure.retryable()
	review.Reason = scheduledTestRenderFailureReason(failure.code)
	return nil
}

func scheduledTestRenderFailureReason(code string) string {
	switch code {
	case "renderer_busy":
		return "后台截图服务繁忙，本次截图未完成，可人工复核或等待下一轮检测"
	case "renderer_unavailable":
		return "后台截图服务连接失败，请检查渲染器运行状态、socket 挂载及权限，或人工复核"
	case "renderer_browser_unavailable":
		return "后台截图浏览器无法启动或完成清理，请检查渲染器部署、资源和沙箱环境，或人工复核"
	case "renderer_timeout":
		return "后台截图超时，本次自动评审未完成，可人工复核或等待下一轮检测"
	case "renderer_invalid_input":
		return "作品未满足后台截图输入要求（内容、大小或运行限制），请人工复核"
	case "renderer_invalid_response":
		return "后台截图结果不完整或格式不符，请检查渲染器版本或人工复核"
	case "renderer_cancelled":
		return "后台截图任务已取消，本次自动评审未完成，可人工复核或等待下一轮检测"
	default:
		return "后台未能完成作品截图，请检查渲染器诊断或人工复核"
	}
}
