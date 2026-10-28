package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func validScheduledTestVisualJSON() string {
	return `{"verdict":"pass","confidence":0.96,"reason":"五项均符合","criteria":[{"key":"pelican","passed":true,"reason":"长喙与喉囊清晰"},{"key":"bicycle","passed":true,"reason":"自行车结构完整"},{"key":"riding","passed":true,"reason":"骑乘关系合理"},{"key":"motion","passed":true,"reason":"轮子和脚踏协调运动"},{"key":"complete","passed":true,"reason":"画面完整"}]}`
}

func reviewTestFrames(t *testing.T) []ScheduledTestReviewFrame {
	t.Helper()
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 960, 720))))
	raw := base64.StdEncoding.EncodeToString(encoded.Bytes())
	return []ScheduledTestReviewFrame{{AtMS: 0, PNGBase64: raw}, {AtMS: 700, PNGBase64: raw}, {AtMS: 1700, PNGBase64: raw}, {AtMS: 2900, PNGBase64: raw}}
}

func TestScheduledTestVisualVerdictRequiresCompleteConsistentEvidence(t *testing.T) {
	valid := validScheduledTestVisualJSON()
	verdict, err := parseScheduledTestVisualVerdict(valid)
	require.NoError(t, err)
	require.Equal(t, "pass", verdict.Verdict)
	require.Len(t, verdict.Criteria, 5)
	failure := strings.Replace(strings.Replace(valid, `"verdict":"pass"`, `"verdict":"fail"`, 1), `"key":"motion","passed":true`, `"key":"motion","passed":false`, 1)
	verdict, err = parseScheduledTestVisualVerdict(failure)
	require.NoError(t, err)
	require.Equal(t, "fail", verdict.Verdict)
	verdict, err = parseScheduledTestVisualVerdict(strings.Replace(valid, "0.96", "0.84", 1))
	require.NoError(t, err)
	require.Equal(t, "pending", verdict.Verdict)
	for name, raw := range map[string]string{
		"missing_confidence":  strings.Replace(valid, `"confidence":0.96,`, "", 1),
		"missing_boolean":     strings.Replace(valid, `"passed":true,`, "", 1),
		"unknown_field":       strings.Replace(valid, `"verdict":"pass"`, `"verdict":"pass","score":100`, 1),
		"duplicate_field":     strings.Replace(valid, `"verdict":"pass"`, `"verdict":"fail","verdict":"pass"`, 1),
		"duplicate_criterion": strings.Replace(valid, `"key":"complete"`, `"key":"motion"`, 1),
		"contradictory_pass":  strings.Replace(valid, `"key":"motion","passed":true`, `"key":"motion","passed":false`, 1),
		"contradictory_fail":  strings.Replace(valid, `"verdict":"pass"`, `"verdict":"fail"`, 1),
		"extra_json":          valid + ` {"verdict":"pass"}`,
		"markdown":            "```json\n" + valid + "\n```",
		"empty":               `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseScheduledTestVisualVerdict(raw)
			require.ErrorIs(t, err, ErrScheduledTestReviewJudgeInvalid)
		})
	}
}

func TestScheduledTestReviewImagesRejectUnusableFrames(t *testing.T) {
	frames := reviewTestFrames(t)
	images, prompt, err := scheduledTestReviewImages(frames)
	require.NoError(t, err)
	require.Len(t, images, 4)
	require.True(t, strings.HasPrefix(images[0], "data:image/png;base64,"))
	require.Contains(t, prompt, "700 ms")
	_, _, err = scheduledTestReviewImages(frames[:3])
	require.Error(t, err)
	frames[1].AtMS = 0
	_, _, err = scheduledTestReviewImages(frames)
	require.Error(t, err)
	frames = reviewTestFrames(t)
	frames[2].PNGBase64 = base64.StdEncoding.EncodeToString([]byte("not a PNG"))
	_, _, err = scheduledTestReviewImages(frames)
	require.Error(t, err)
}

func TestScheduledTestReviewJudgeWireCarriesImagesAndUsesQualityBudget(t *testing.T) {
	for _, responses := range []bool{true, false} {
		t.Run(map[bool]string{true: "responses", false: "chat"}[responses], func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: responses}}
			repo := &modelIdentityAccountRepo{account: account}
			verdictText := validScheduledTestVisualJSON()
			body := map[string]any{"model": "judge-model", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": verdictText}}}}, "usage": map[string]any{"input_tokens": 50, "output_tokens": 20}}
			if !responses {
				body = map[string]any{"model": "judge-model", "choices": []any{map[string]any{"message": map[string]any{"content": verdictText}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 50, "completion_tokens": 20}}
			}
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			admissions := 0
			ops := &qualityOperationsStub{reserve: func(ctx context.Context, id, tokens int64, automatic bool, session string) (*QualityProbeReservation, error) {
				admissions++
				require.Equal(t, int64(42), id)
				require.Positive(t, tokens)
				require.True(t, automatic)
				require.NotEqual(t, "measured-account-session", session)
				require.Equal(t, "regular", QualityProbePurpose(ctx))
				return &QualityProbeReservation{ID: 1, AccountID: id}, nil
			}}
			var requestPayload map[string]any
			upstream := &modelIdentityHTTPUpstream{body: string(raw), beforeReturn: func(req *http.Request, _ int) {
				body, err := req.GetBody()
				require.NoError(t, err)
				defer func() { require.NoError(t, body.Close()) }()
				require.NoError(t, json.NewDecoder(body).Decode(&requestPayload))
			}}
			testSvc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}, qualityOperations: ops}
			ctx := context.WithValue(WithQualityRecoveryProbe(context.Background()), qualityRunKey{}, "measured-account-session")
			verdict, err := NewScheduledTestReviewJudge(testSvc, repo).Judge(ctx, 42, "judge-model", reviewTestFrames(t))
			require.NoError(t, err)
			require.Equal(t, "pass", verdict.Verdict)
			require.Equal(t, 1, admissions)
			require.Len(t, ops.outcomes, 1)
			require.NotNil(t, ops.outcomes[0].Tokens)
			require.Equal(t, int64(70), *ops.outcomes[0].Tokens)
			encoded, err := json.Marshal(requestPayload)
			require.NoError(t, err)
			require.Equal(t, 4, strings.Count(string(encoded), "data:image/png;base64,"))
			require.Contains(t, string(encoded), "untrusted artwork")
			if responses {
				require.Equal(t, float64(2048), requestPayload["max_output_tokens"])
				require.Contains(t, string(encoded), `"type":"input_image"`)
			} else {
				require.Equal(t, float64(2048), requestPayload["max_tokens"])
				require.Contains(t, string(encoded), `"type":"image_url"`)
			}
		})
	}
}

func TestScheduledTestReviewJudgeBudgetDenialNeverCallsUpstream(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	repo := &modelIdentityAccountRepo{account: account}
	upstream := &modelIdentityHTTPUpstream{}
	ops := &qualityOperationsStub{reserve: func(context.Context, int64, int64, bool, string) (*QualityProbeReservation, error) {
		return nil, &QualityDeferredError{Reason: "budget", Until: time.Now().Add(time.Hour)}
	}}
	testSvc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}, qualityOperations: ops}
	_, err := NewScheduledTestReviewJudge(testSvc, repo).Judge(context.Background(), 42, "judge-model", reviewTestFrames(t))
	require.ErrorIs(t, err, ErrScheduledTestReviewJudgeUnavailable)
	require.Zero(t, upstream.requests)
	require.Empty(t, ops.outcomes)
}

func TestScheduledTestReviewJudgeRejectsUnsupportedOrDisabledAccount(t *testing.T) {
	for _, account := range []*Account{
		{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: "disabled", Schedulable: true},
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: false},
	} {
		repo := &modelIdentityAccountRepo{account: account}
		testSvc := &AccountTestService{qualityOperations: &qualityOperationsStub{}}
		_, err := NewScheduledTestReviewJudge(testSvc, repo).Judge(context.Background(), 1, "judge-model", reviewTestFrames(t))
		require.ErrorIs(t, err, ErrScheduledTestReviewJudgeUnavailable)
	}
}

func TestVisualReviewBudgetEstimateDoesNotTreatCompressionBytesAsText(t *testing.T) {
	for _, imageLength := range []int{1000, 1000000} {
		images := []string{strings.Repeat("x", imageLength), strings.Repeat("x", imageLength), strings.Repeat("x", imageLength), strings.Repeat("x", imageLength)}
		ctx := context.WithValue(context.Background(), accountTestVisionContextKey{}, accountTestVisionInput{Images: images})
		ctx = withAccountTestMaxOutputTokens(ctx, 2048)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.example.com", nil)
		require.NoError(t, err)
		req.ContentLength = int64(imageLength*4 + 5000)
		require.Equal(t, int64(4*65536+5000+2048), qualityProbeTokenEstimate(req))
	}
	request, err := http.NewRequest(http.MethodPost, "https://upstream.example.com", nil)
	require.NoError(t, err)
	request.ContentLength = 12345
	require.Equal(t, int64(12345), qualityProbeTokenEstimate(request))
	request.ContentLength = -1
	require.Zero(t, qualityProbeTokenEstimate(request))
	request.ContentLength = math.MaxInt64
	request = request.WithContext(withAccountTestMaxOutputTokens(context.Background(), 2048))
	require.Equal(t, int64(math.MaxInt64), qualityProbeTokenEstimate(request))
}

func TestVisualReviewBudgetEstimateUsesActualWireModel(t *testing.T) {
	for _, test := range []struct {
		model    string
		perImage int64
	}{
		{"gpt-4o-mini-2024-07-18", 32768},
		{"gpt-4o", 2048},
		{"gpt-4.1", 2048},
		{"o3", 2048},
		{"gpt-4.1-mini", 8192},
		{"o4-mini", 8192},
		{"gpt-5.4", 8192},
		{"gpt-6-sol", 8192},
		{"custom-vision-model", 65536},
	} {
		t.Run(test.model, func(t *testing.T) {
			ctx, identity := newAccountTestModelIdentity(context.Background())
			identity.upstream = test.model
			ctx = context.WithValue(ctx, accountTestVisionContextKey{}, accountTestVisionInput{Images: []string{"image1", "image2", "image3", "image4"}})
			ctx = withAccountTestMaxOutputTokens(ctx, 2048)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.example.com", nil)
			require.NoError(t, err)
			req.ContentLength = 5024
			require.Equal(t, test.perImage*4+5000+2048, qualityProbeTokenEstimate(req))
		})
	}
}
