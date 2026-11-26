package httpapi

// goca:manual — настройки роутера (405/404, trusted proxies) для контракта BC#2.

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// NewRouter собирает API-роутер. Настройки маршрутизации общие для main и тестов:
//   - 405 на неподдерживаемый метод;
//   - 404 на trailing slash и percent-encoded путь (никаких редиректов);
//   - доверенные прокси отключены (сервис стоит за API Gateway).
func NewRouter(healthHandler interface{ Register(gin.IRouter) }) *gin.Engine {
	router := newEngine()
	healthHandler.Register(router)
	return router
}

// NewFullRouter — API-роутер BC#2 со всеми бизнес-маршрутами.
func NewFullRouter(
	healthHandler interface{ Register(gin.IRouter) },
	billing *BillingHandler,
	authMiddleware gin.HandlerFunc,
	idempotency func(required bool) gin.HandlerFunc,
	corsOrigins []string,
	maxBody int64,
) *gin.Engine {
	router := newEngine()
	if len(corsOrigins) > 0 {
		router.Use(CORS(corsOrigins))
	}
	if maxBody > 0 {
		router.Use(MaxBody(maxBody))
	}
	healthHandler.Register(router)

	billingGroup := router.Group("/billing")
	{
		billingGroup.POST("/subscriptions", authMiddleware, idempotency(true), billing.Subscribe)
		billingGroup.GET("/subscriptions/:subscriptionId", authMiddleware, billing.GetSubscription)
		billingGroup.PATCH("/subscriptions/:subscriptionId", authMiddleware, billing.PatchSubscription)
		billingGroup.GET("/subscriptions/:subscriptionId/upgrade/preview", authMiddleware, billing.PreviewUpgrade)
		billingGroup.POST("/subscriptions/:subscriptionId/upgrade", authMiddleware, idempotency(true), billing.Upgrade)
		billingGroup.POST("/subscriptions/:subscriptionId/downgrade", authMiddleware, idempotency(true), billing.Downgrade)
		billingGroup.POST("/subscriptions/:subscriptionId/cancel", authMiddleware, idempotency(false), billing.Cancel)
		billingGroup.POST("/subscriptions/:subscriptionId/resume", authMiddleware, idempotency(false), billing.Resume)
		billingGroup.GET("/subscriptions/:subscriptionId/payments", authMiddleware, billing.ListPayments)

		// Вебхук без JWT: подпись HMAC проверяется в usecase.
		billingGroup.POST("/webhooks/acquiring", billing.AcquiringWebhook)
	}

	adminGroup := router.Group("/admin/billing", authMiddleware)
	{
		adminGroup.POST("/accounts/:accountId/activate-tariff", idempotency(true), billing.AdminActivateTariff)
		adminGroup.POST("/accounts/:accountId/deactivate-tariff", idempotency(true), billing.AdminDeactivateTariff)
	}
	return router
}

func newEngine() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery(), TracingMiddleware())
	router.HandleMethodNotAllowed = true
	router.RedirectTrailingSlash = false
	router.UseRawPath = true
	router.UnescapePathValues = false
	_ = router.SetTrustedProxies(nil) // nil = не доверять X-Forwarded-For
	return router
}

// NewAdminMux — служебный сервер: метрики и liveness.
func NewAdminMux(metricsHandler, liveHandler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metricsHandler)
	mux.Handle("/health/live", liveHandler)
	return mux
}
