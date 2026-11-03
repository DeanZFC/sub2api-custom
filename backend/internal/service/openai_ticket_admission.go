package service

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// OpenAITurnAdmissionError rejects a request before sending it upstream. It must
// not update upstream health, rate limits, or account cooldowns.
type OpenAITurnAdmissionError struct {
	Reason string
	cause  error
}

func (e *OpenAITurnAdmissionError) Error() string { return "request admission denied: " + e.Reason }
func (e *OpenAITurnAdmissionError) Unwrap() error { return e.cause }

func IsOpenAITurnAdmissionError(err error) bool {
	var denied *OpenAITurnAdmissionError
	return errors.As(err, &denied)
}

func denyOpenAITurn(reason string) error { return &OpenAITurnAdmissionError{Reason: reason} }

func denyOpenAITicket() error {
	return &OpenAITurnAdmissionError{Reason: "model_ticket_unavailable", cause: ErrOpenAICodexTicketUnavailable}
}

// WriteOpenAITurnAdmissionError writes a retryable pre-send failure for HTTP
// transports. WebSocket transports may translate the same error to their own
// error frame. The helper never writes over an already-started response.
func WriteOpenAITurnAdmissionError(c *gin.Context, err error) bool {
	var denied *OpenAITurnAdmissionError
	if !errors.As(err, &denied) || c == nil {
		return false
	}
	if !c.Writer.Written() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"type":    "server_error",
			"code":    denied.Reason,
			"message": "No eligible Codex account with a valid model ticket is available",
		}})
	}
	return true
}
