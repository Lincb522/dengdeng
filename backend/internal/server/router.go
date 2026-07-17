package server

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/gateway"
	"dengdeng/internal/handler"
	"dengdeng/internal/middleware"
	"dengdeng/internal/oauth"
	"dengdeng/internal/service"
	"dengdeng/internal/web"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func NewRouter(cfg *config.Config, db *gorm.DB) *gin.Engine {
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		panic("invalid SERVER_TRUSTED_PROXIES: " + err.Error())
	}
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.PublicCORS())

	pricing := service.NewPricingService(db)
	payments := service.NewPaymentService(db, cfg)
	payments.StartReconciler()
	runtimePolicy := service.NewRuntimePolicyService(db)
	audit := service.NewAuditService(db)
	alertService := service.NewAlertService(db, service.NewSMTPMailer(cfg.SMTP, cfg.Site.Name, cfg.Site.PublicURL), cfg.Admin.Email)
	backupService := service.NewBackupService(db, cfg)
	scheduler := service.NewScheduler(db)
	scheduler.SetRuntimePolicy(runtimePolicy)
	billing := service.NewBillingService(db, pricing)
	rates := service.NewUserGroupRateResolver(db)
	runtimeMetrics := service.NewRuntimeMetrics()
	providerClient, err := cfg.Proxy.HTTPClient(0)
	if err != nil {
		panic(err)
	}
	oauthClient, err := cfg.Proxy.HTTPClient(30 * time.Second)
	if err != nil {
		panic(err)
	}
	oauthManager := oauth.NewManager(db, cfg.OAuth, oauthClient)
	accountQuota := service.NewAccountQuotaService(db, cfg, oauthManager, oauthClient)
	gw := gateway.New(db, scheduler, billing, rates, oauthManager, runtimeMetrics, providerClient)
	gw.SetRuntimePolicy(runtimePolicy)

	authH := handler.NewAuthHandler(db, cfg)
	userH := handler.NewUserHandler(db, cfg)
	adminH := handler.NewAdminHandler(db, pricing, oauthManager, rates)
	adminH.SetCodexQuotaHTTPClient(oauthClient)
	adminH.SetAccountQuotaService(accountQuota)
	accountMonitor := service.NewAccountMonitor(db, cfg)
	accountMonitor.SetRuntimePolicy(runtimePolicy)
	accountMonitor.SetAlertService(alertService)
	accountMonitor.SetOAuthManager(oauthManager)
	accountMonitor.SetQuotaService(accountQuota)
	adminH.SetAccountMonitor(accountMonitor)
	adminH.SetRuntimeMetrics(runtimeMetrics)
	systemSettingsH := handler.NewSystemSettingsHandler(db, cfg)
	systemSettingsH.SetAuditService(audit)
	runtimeSettingsH := handler.NewRuntimeSettingsHandler(db, runtimePolicy, audit)
	alertH := handler.NewAlertHandler(db, audit)
	backupH := handler.NewBackupHandler(backupService, audit)
	paymentH := handler.NewPaymentHandler(payments)
	adminPaymentH := handler.NewAdminPaymentHandler(payments)

	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// Relay endpoints (client API keys)
	gw.Register(r)

	// Console API (JWT). Cap request bodies; the relay sets its own limit.
	api := r.Group("/api", middleware.MaxBodyBytes(1<<20))
	{
		// Payment providers authenticate their own signed callbacks; they must
		// not carry a console JWT. The handler enforces a 1 MB body cap again.
		api.POST("/payment/webhook/:provider", paymentH.Webhook)
		// Throttle unauthenticated auth endpoints to blunt credential stuffing.
		authGroup := api.Group("", middleware.RateLimit(20, time.Minute))
		authGroup.GET("/settings", authH.PublicSettings)
		authGroup.POST("/auth/register/code", authH.SendRegistrationCode)
		authGroup.POST("/auth/register", authH.Register)
		authGroup.POST("/auth/login", authH.Login)
		// Provider callback cannot carry the console's JWT. Its one-time PKCE
		// state is validated by AdminHandler before any account is created.
		api.GET("/admin/oauth/:platform/callback", adminH.CompleteOAuthLogin)

		user := api.Group("/user", middleware.JWTAuth(db, cfg.JWT.Secret))
		{
			user.GET("/me", userH.Me)
			user.POST("/password", userH.ChangePassword)
			user.GET("/groups", userH.ListGroups)
			user.GET("/keys", userH.ListKeys)
			user.POST("/keys", userH.CreateKey)
			user.PUT("/keys/:id", userH.UpdateKey)
			user.POST("/keys/:id/rotate", userH.RotateKey)
			user.DELETE("/keys/:id", userH.DeleteKey)
			user.GET("/model-catalog", userH.ModelCatalogue)
			user.GET("/usage", userH.Usage)
			user.GET("/usage/export", userH.ExportUsage)
			user.GET("/usage/summary", userH.UsageSummary)
			user.GET("/referrals", userH.ReferralDashboard)
			user.POST("/referrals/code", userH.CreateMyReferralCode)
			user.POST("/referrals/bind", userH.BindReferralCode)
			user.POST("/redeem", userH.Redeem)
			user.GET("/payment/config", paymentH.Config)
			user.POST("/payment/orders", paymentH.CreateOrder)
			user.GET("/payment/orders", paymentH.ListMyOrders)
			user.GET("/payment/orders/:id", paymentH.GetOrder)
			user.POST("/payment/orders/:id/verify", paymentH.VerifyOrder)
			user.POST("/payment/orders/:id/cancel", paymentH.CancelOrder)
			user.POST("/payment/orders/:id/refund-request", paymentH.RequestRefund)
		}

		admin := api.Group("/admin", middleware.JWTAuth(db, cfg.JWT.Secret), middleware.AdminOnly())
		{
			admin.GET("/dashboard", adminH.Dashboard)
			admin.GET("/usage", adminH.Usage)
			admin.GET("/usage/export", adminH.ExportUsage)
			admin.GET("/ops/snapshot", adminH.OpsSnapshot)
			admin.POST("/ops/probe", adminH.TriggerAccountProbes)
			admin.POST("/ops/accounts/:id/probe", adminH.ProbeAccount)
			admin.GET("/users", adminH.ListUsers)
			admin.PUT("/users/:id", adminH.UpdateUser)
			admin.GET("/users/:id/group-rates", adminH.ListUserGroupRates)
			admin.PUT("/users/:id/group-rates", adminH.ReplaceUserGroupRates)
			admin.GET("/groups", adminH.ListGroups)
			admin.POST("/groups", adminH.CreateGroup)
			admin.PUT("/groups/:id", adminH.UpdateGroup)
			admin.DELETE("/groups/:id", adminH.DeleteGroup)
			admin.GET("/accounts", adminH.ListAccounts)
			admin.POST("/accounts", adminH.CreateAccount)
			admin.POST("/accounts/import", adminH.ImportAccounts)
			admin.PUT("/accounts/order", adminH.ReorderAccounts)
			admin.POST("/accounts/:id/quota/refresh", adminH.RefreshAccountQuota)
			admin.POST("/accounts/:id/codex-quota/refresh", adminH.RefreshCodexQuota)
			admin.POST("/oauth/:platform/start", adminH.StartOAuthLogin)
			admin.PUT("/accounts/:id", adminH.UpdateAccount)
			admin.DELETE("/accounts/:id", adminH.DeleteAccount)
			admin.GET("/proxies", adminH.ListProxies)
			admin.POST("/proxies", adminH.CreateProxy)
			admin.PUT("/proxies/:id", adminH.UpdateProxy)
			admin.DELETE("/proxies/:id", adminH.DeleteProxy)
			admin.POST("/proxies/:id/test", adminH.TestProxy)
			admin.GET("/settings", systemSettingsH.Get)
			admin.PUT("/settings", systemSettingsH.Update)
			admin.GET("/runtime-settings", runtimeSettingsH.Get)
			admin.PUT("/runtime-settings", runtimeSettingsH.Update)
			admin.GET("/audit-logs", runtimeSettingsH.ListAuditLogs)
			admin.GET("/alerts/rules", alertH.ListRules)
			admin.POST("/alerts/rules", alertH.CreateRule)
			admin.PUT("/alerts/rules/:id", alertH.UpdateRule)
			admin.DELETE("/alerts/rules/:id", alertH.DeleteRule)
			admin.GET("/alerts/events", alertH.ListEvents)
			admin.POST("/alerts/events/:id/acknowledge", alertH.AcknowledgeEvent)
			admin.GET("/channel-monitor/history", alertH.ChannelHistory)
			admin.GET("/backups", backupH.List)
			admin.POST("/backups", backupH.Create)
			admin.GET("/backups/:id/download", backupH.Download)
			admin.DELETE("/backups/:id", backupH.Delete)
			admin.GET("/prices", adminH.ListPrices)
			admin.POST("/prices", adminH.UpsertPrice)
			admin.DELETE("/prices/:id", adminH.DeletePrice)
			admin.GET("/models", adminH.ListModels)
			admin.POST("/models", adminH.UpsertModel)
			admin.DELETE("/models/:id", adminH.DeleteModel)
			admin.GET("/redeem-codes", adminH.ListRedeemCodes)
			admin.POST("/redeem-codes", adminH.GenerateRedeemCodes)
			admin.DELETE("/redeem-codes/:id", adminH.DeleteRedeemCode)
			admin.GET("/referral-codes", adminH.ListReferralCodes)
			admin.POST("/referral-codes", adminH.CreateReferralCode)
			admin.PUT("/referral-codes/:id", adminH.UpdateReferralCode)
			admin.DELETE("/referral-codes/:id", adminH.DeleteReferralCode)
			admin.GET("/payment/config", adminPaymentH.GetConfig)
			admin.PUT("/payment/config", adminPaymentH.UpdateConfig)
			admin.GET("/payment/providers", adminPaymentH.ListProviders)
			admin.POST("/payment/providers", adminPaymentH.CreateProvider)
			admin.PUT("/payment/providers/:id", adminPaymentH.UpdateProvider)
			admin.DELETE("/payment/providers/:id", adminPaymentH.DeleteProvider)
			admin.GET("/payment/orders", adminPaymentH.ListOrders)
			admin.POST("/payment/orders/:id/refund", adminPaymentH.ProcessRefund)
			admin.POST("/payment/orders/:id/refund/query", adminPaymentH.FinalizeRefund)
		}
	}

	mountFrontend(r)
	accountMonitor.Start()
	return r
}

// mountFrontend serves the embedded SPA: real files as-is, everything else
// falls back to index.html for client-side routing.
func mountFrontend(r *gin.Engine) {
	dist, err := web.Dist()
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(dist))
	r.NoRoute(func(c *gin.Context) {
		p := strings.TrimPrefix(c.Request.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err == nil {
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}
