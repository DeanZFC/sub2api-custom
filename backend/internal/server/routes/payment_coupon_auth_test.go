package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCouponRoutesEnforceUserAndAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterPaymentRoutes(router.Group("/api/v1"), &handler.PaymentHandler{}, &handler.PaymentWebhookHandler{}, &admin.PaymentHandler{},
		servermiddleware.JWTAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }),
		servermiddleware.AdminAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) }),
		servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }), nil, nil, nil)

	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/payment/coupons", http.StatusUnauthorized},
		{http.MethodGet, "/payment/coupons/usage", http.StatusUnauthorized},
		{http.MethodPost, "/payment/coupons/1/quote", http.StatusUnauthorized},
		{http.MethodGet, "/admin/payment/coupons", http.StatusForbidden},
		{http.MethodGet, "/admin/payment/coupons/usage", http.StatusForbidden},
		{http.MethodGet, "/admin/payment/coupons/templates", http.StatusForbidden},
		{http.MethodPost, "/admin/payment/coupons/templates", http.StatusForbidden},
		{http.MethodPut, "/admin/payment/coupons/templates/1", http.StatusForbidden},
		{http.MethodPost, "/admin/payment/coupons/issue", http.StatusForbidden},
		{http.MethodPost, "/admin/payment/coupons/1/revoke", http.StatusForbidden},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, "/api/v1"+tc.path, nil))
			require.Equal(t, tc.status, w.Code)
		})
	}
}
