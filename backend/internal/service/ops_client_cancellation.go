package service

import "github.com/gin-gonic/gin"

// OpsClientCanceledCode denotes a downstream request outcome, never an upstream rejection.
const OpsClientCanceledCode = "client_canceled"

// MarkOpsClientCancellation records a logical 499 without writing to a closed client.
func MarkOpsClientCancellation(c *gin.Context, stream bool) {
	MarkOpsStreamErrorValue(c, OpsStreamError{
		ErrType: OpsClientCanceledCode, Code: OpsClientCanceledCode,
		Message: "Client disconnected: context canceled", IntendedStatus: 499,
		RequestScoped: true, NonStream: !stream,
	})
}
