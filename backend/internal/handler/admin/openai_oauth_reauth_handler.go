package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const openAIOAuthReauthWorkerTokenEnv = "OPENAI_REAUTH_WORKER_TOKEN"

type OpenAIOAuthReauthHandler struct {
	service *service.OpenAIOAuthReauthService
}

func NewOpenAIOAuthReauthHandler(reauthService *service.OpenAIOAuthReauthService) *OpenAIOAuthReauthHandler {
	return &OpenAIOAuthReauthHandler{service: reauthService}
}

type saveOpenAIOAuthReauthConfigRequest struct {
	LoginEmail     string `json:"login_email" binding:"required"`
	CredentialMode string `json:"credential_mode"`
	Engine         string `json:"engine"`
	Password       string `json:"password"`
	TOTPSecret     string `json:"totp_secret"`
	ProxySource    string `json:"proxy_source"`
	ProxyID        *int64 `json:"proxy_id"`
	ClearPassword  bool   `json:"clear_password"`
	ClearTOTP      bool   `json:"clear_totp"`
}

func parseReauthAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}

func (h *OpenAIOAuthReauthHandler) SaveConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	accountID, ok := parseReauthAccountID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var req saveOpenAIOAuthReauthConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	config, err := h.service.SaveCredentialConfig(c.Request.Context(), accountID, service.OpenAIOAuthReauthConfigInput{
		LoginEmail: req.LoginEmail, CredentialMode: req.CredentialMode, Engine: req.Engine,
		Password: req.Password, TOTPSecret: req.TOTPSecret, ProxySource: req.ProxySource,
		ProxyID: req.ProxyID, ClearPassword: req.ClearPassword, ClearTOTP: req.ClearTOTP,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, config)
}

func (h *OpenAIOAuthReauthHandler) GetStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	accountID, ok := parseReauthAccountID(c)
	if !ok {
		return
	}
	status, err := h.service.GetStatus(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

func (h *OpenAIOAuthReauthHandler) CreateTask(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	accountID, ok := parseReauthAccountID(c)
	if !ok {
		return
	}
	profile := "excel"
	if c.Request.ContentLength != 0 {
		var req struct {
			OAuthProfile string `json:"oauth_profile"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid authorization request")
			return
		}
		if req.OAuthProfile != "" {
			profile = req.OAuthProfile
		}
	}
	task, err := h.service.CreateTaskForProfile(c.Request.Context(), accountID, profile)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, task)
}

type reauthWorkerRequest struct {
	WorkerID string   `json:"worker_id"`
	Engines  []string `json:"engines"`
}

type reauthWorkerProgressRequest struct {
	WorkerID string `json:"worker_id"`
	Stage    string `json:"stage" binding:"required"`
}

type reauthWorkerCallbackRequest struct {
	WorkerID    string `json:"worker_id"`
	CallbackURL string `json:"callback_url" binding:"required"`
}

type reauthWorkerCredentialsRequest struct {
	WorkerID    string         `json:"worker_id"`
	Credentials map[string]any `json:"credentials" binding:"required"`
	Extra       map[string]any `json:"extra"`
}

type reauthWorkerFailureRequest struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

func (h *OpenAIOAuthReauthHandler) requireWorker(c *gin.Context) bool {
	configured, valid := h.service.WorkerAuthentication(strings.TrimSpace(c.GetHeader("X-OpenAI-Reauth-Worker-Token")))
	if !configured {
		response.Error(c, http.StatusServiceUnavailable, "OpenAI re-auth worker is not configured")
		c.Abort()
		return false
	}
	if !valid {
		response.Unauthorized(c, "invalid OpenAI re-auth worker token")
		c.Abort()
		return false
	}
	return true
}

// Claim is intentionally outside the admin-authenticated route group. It is
// protected by a separate high-entropy worker token and only returns a task
// when one is queued.
func (h *OpenAIOAuthReauthHandler) Claim(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !h.requireWorker(c) {
		return
	}
	var req reauthWorkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid worker request")
		return
	}
	claim, err := h.service.ClaimTaskWithEngines(c.Request.Context(), req.WorkerID, req.Engines)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, claim)
}

func (h *OpenAIOAuthReauthHandler) RuntimeSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !h.requireWorker(c) {
		return
	}
	h.service.MarkWorkerSeen()
	cfg, err := h.service.GetRuntimeSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var count *int
	if cfg.ConcurrencyConfigured {
		count = &cfg.WorkerConcurrency
	}
	response.Success(c, gin.H{"worker_concurrency": count})
}

func (h *OpenAIOAuthReauthHandler) Progress(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !h.requireWorker(c) {
		return
	}
	taskID, err := strconv.ParseInt(strings.TrimSpace(c.Param("task_id")), 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task ID")
		return
	}
	var req reauthWorkerProgressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid worker request")
		return
	}
	if err := h.service.UpdateStage(c.Request.Context(), taskID, req.WorkerID, req.Stage); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"accepted": true})
}

func (h *OpenAIOAuthReauthHandler) Credentials(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !h.requireWorker(c) {
		return
	}
	taskID, err := strconv.ParseInt(strings.TrimSpace(c.Param("task_id")), 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task ID")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	var req reauthWorkerCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid worker request")
		return
	}
	task, err := h.service.SubmitCredentials(c.Request.Context(), taskID, req.WorkerID, req.Credentials, req.Extra)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, task)
}

func (h *OpenAIOAuthReauthHandler) Fail(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !h.requireWorker(c) {
		return
	}
	taskID, err := strconv.ParseInt(strings.TrimSpace(c.Param("task_id")), 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task ID")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req reauthWorkerFailureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid worker request")
		return
	}
	if err := h.service.FailTask(c.Request.Context(), taskID, req.WorkerID, req.Reason); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"accepted": true})
}

// WorkerStatus exposes only runtime health; paths, credentials and worker keys
// never leave the backend.
func (h *OpenAIOAuthReauthHandler) WorkerStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	response.Success(c, h.service.WorkerStatus())
}
