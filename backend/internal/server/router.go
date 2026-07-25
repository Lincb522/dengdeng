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
	"dengdeng/internal/version"
	"dengdeng/internal/web"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func NewRouter(cfg *config.Config, db *gorm.DB) *gin.Engine {
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	systemSettings := service.NewSystemSettingsService(db, cfg)
	realIPSettings, _ := systemSettings.Get()
	trustedProxies := cfg.Server.TrustedProxies
	forwardedHeaders := cfg.Server.ForwardedClientIPHeaders
	if len(realIPSettings.ForwardedClientIPHeaders) > 0 {
		trustedProxies = realIPSettings.TrustedProxies
		forwardedHeaders = realIPSettings.ForwardedClientIPHeaders
	}
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		panic("invalid SERVER_TRUSTED_PROXIES: " + err.Error())
	}
	r.RemoteIPHeaders = append([]string(nil), forwardedHeaders...)
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.PublicCORS())

	pricing := service.NewPricingService(db)
	payments := service.NewPaymentService(db, cfg)
	payments.StartReconciler()
	referralCash := service.NewReferralCashService(db, cfg)
	referralCash.StartReconciler()
	runtimePolicy := service.NewRuntimePolicyService(db)
	audit := service.NewAuditService(db)
	audit.StartRetention(systemSettings)
	runtimeMailer := service.NewRuntimeSMTPMailer(systemSettings, cfg.SMTP, cfg.Site.PublicURL)
	alertService := service.NewAlertService(db, runtimeMailer, cfg.Admin.Email)
	service.NewNotificationScheduler(db, systemSettings, runtimeMailer).Start()
	backupService := service.NewBackupService(db, cfg)
	updateService := service.NewUpdateService(cfg)
	scheduler := service.NewScheduler(db)
	scheduler.SetRuntimePolicy(runtimePolicy)
	billing := service.NewBillingService(db, pricing)
	billing.SetIPGeoResolver(service.NewIPGeoResolver(db))
	billing.SetSystemSettings(systemSettings)
	rates := service.NewUserGroupRateResolver(db)
	runtimeMetrics := service.NewRuntimeMetrics()
	opsCollector := service.NewOpsCollector(db, runtimeMetrics, alertService)
	opsCollector.Start()
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
	imageStorage := service.NewImageStorageService(db, providerClient)
	gw := gateway.New(db, scheduler, billing, rates, oauthManager, runtimeMetrics, providerClient)
	gw.SetRuntimePolicy(runtimePolicy)
	gw.SetSystemSettings(systemSettings)
	gw.SetImageStorageService(imageStorage)
	gw.SetAccountQuotaService(accountQuota)

	authH := handler.NewAuthHandlerWithMailer(db, cfg, runtimeMailer)
	userH := handler.NewUserHandler(db, cfg)
	adminH := handler.NewAdminHandler(db, pricing, oauthManager, rates)
	adminH.SetCodexQuotaHTTPClient(oauthClient)
	adminH.SetAccountQuotaService(accountQuota)
	accountMonitor := service.NewAccountMonitor(db, cfg)
	accountMonitor.SetRuntimePolicy(runtimePolicy)
	accountMonitor.SetAlertService(alertService)
	accountMonitor.SetOAuthManager(oauthManager)
	accountMonitor.SetQuotaService(accountQuota)
	accountMonitor.SetSystemSettings(systemSettings)
	adminH.SetAccountMonitor(accountMonitor)
	adminH.SetRuntimeMetrics(runtimeMetrics)
	adminH.SetScheduler(scheduler)
	adminH.SetImageStorageService(imageStorage)
	systemSettingsH := handler.NewSystemSettingsHandler(db, cfg)
	systemSettingsH.SetAuditService(audit)
	systemSettingsH.SetEngine(r)
	runtimeSettingsH := handler.NewRuntimeSettingsHandler(db, runtimePolicy, audit)
	alertH := handler.NewAlertHandler(db, audit)
	backupH := handler.NewBackupHandler(backupService, audit)
	updateH := handler.NewUpdateHandler(updateService, backupService, audit)
	paymentH := handler.NewPaymentHandler(payments)
	adminPaymentH := handler.NewAdminPaymentHandler(payments)
	referralCashH := handler.NewReferralCashHandler(referralCash)

	r.GET("/health", func(c *gin.Context) {
		build := version.Info()
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": build.Version, "commit": build.Commit, "build_time": build.BuildTime})
	})

	// Relay endpoints (client API keys)
	gw.Register(r)

	// Console API (JWT). Cap request bodies; the relay sets its own limit.
	api := r.Group("/api", middleware.MaxBodyBytes(1<<20))
	{
		// Payment providers authenticate their own signed callbacks; they must
		// not carry a console JWT. The handler enforces a 1 MB body cap again.
		api.POST("/payment/webhook/:provider", paymentH.Webhook)
		api.POST("/referrals/payout/webhook/wxpay", referralCashH.WxPayWebhook)
		// The public model plaza is intentionally available without a console
		// account and has a read-oriented limit separate from login attempts.
		api.GET("/models", middleware.RateLimit(120, time.Minute), userH.PublicModelCatalogue)
		// Throttle unauthenticated auth endpoints to blunt credential stuffing.
		authGroup := api.Group("", middleware.RateLimit(20, time.Minute))
		authGroup.GET("/settings", authH.PublicSettings)
		authGroup.POST("/auth/register/code", authH.SendRegistrationCode)
		authGroup.POST("/auth/register", authH.Register)
		authGroup.POST("/auth/login", authH.Login)
		authGroup.POST("/auth/password-reset/code", authH.SendPasswordResetCode)
		authGroup.POST("/auth/password-reset", authH.ResetPassword)
		authGroup.POST("/auth/oauth/:provider/start", authH.StartUserOAuth)
		authGroup.GET("/auth/oauth/:provider/callback", authH.CompleteUserOAuth)
		authGroup.POST("/auth/oauth/exchange", authH.ExchangeUserOAuth)
		// Provider callback cannot carry the console's JWT. Its one-time PKCE
		// state is validated by AdminHandler before any account is created.
		api.GET("/admin/oauth/:platform/callback", adminH.CompleteOAuthLogin)

		user := api.Group("/user", middleware.JWTAuth(db, cfg.JWT.Secret, systemSettings))
		{
			stepUp := middleware.RequireStepUp(systemSettings)
			user.GET("/me", userH.Me)
			user.POST("/step-up", userH.StepUp)
			user.POST("/password", userH.ChangePassword)
			user.POST("/totp/setup", userH.SetupTOTP)
			user.POST("/totp/enable", userH.EnableTOTP)
			user.POST("/totp/disable", userH.DisableTOTP)
			user.GET("/groups", userH.ListGroups)
			user.GET("/keys", userH.ListKeys)
			user.POST("/keys", userH.CreateKey)
			user.PUT("/keys/:id", userH.UpdateKey)
			user.POST("/keys/:id/rotate", stepUp, userH.RotateKey)
			user.DELETE("/keys/:id", userH.DeleteKey)
			user.GET("/model-catalog", userH.ModelCatalogue)
			user.GET("/usage", userH.Usage)
			user.GET("/usage/export", userH.ExportUsage)
			user.GET("/usage/summary", userH.UsageSummary)
			user.GET("/referrals", userH.ReferralDashboard)
			user.POST("/referrals/code", userH.CreateMyReferralCode)
			user.POST("/referrals/bind", userH.BindReferralCode)
			user.POST("/referrals/payout-account", referralCashH.SaveMyPayoutAccount)
			user.GET("/referrals/payouts", referralCashH.ListMyPayouts)
			user.POST("/referrals/payouts", referralCashH.RequestPayout)
			user.POST("/redeem", userH.Redeem)
			user.GET("/payment/config", paymentH.Config)
			user.POST("/payment/orders", paymentH.CreateOrder)
			user.GET("/payment/orders", paymentH.ListMyOrders)
			user.GET("/payment/orders/:id", paymentH.GetOrder)
			user.POST("/payment/orders/:id/verify", paymentH.VerifyOrder)
			user.POST("/payment/orders/:id/cancel", paymentH.CancelOrder)
			user.POST("/payment/orders/:id/refund-request", paymentH.RequestRefund)
		}

		admin := api.Group("/admin", middleware.JWTAuth(db, cfg.JWT.Secret, systemSettings), middleware.AdminOnly())
		{
			stepUp := middleware.RequireStepUp(systemSettings)
			admin.GET("/dashboard", adminH.Dashboard)
			admin.GET("/usage", adminH.Usage)
			admin.GET("/usage/export", adminH.ExportUsage)
			admin.GET("/ops/snapshot", adminH.OpsSnapshot)
			admin.GET("/ops/errors", adminH.OpsErrors)
			admin.POST("/ops/errors/:id/resolve", adminH.ResolveOpsError)
			admin.GET("/ops/system-metrics", adminH.OpsSystemHistory)
			admin.GET("/ops/job-heartbeats", adminH.OpsJobHeartbeats)
			admin.GET("/ops/system-logs", adminH.OpsSystemLogs)
			admin.DELETE("/ops/system-logs", adminH.ClearOpsSystemLogs)
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
			admin.PUT("/settings", stepUp, systemSettingsH.Update)
			admin.POST("/settings/email/test", systemSettingsH.TestEmail)
			admin.GET("/image-storage", adminH.GetImageStorage)
			admin.PUT("/image-storage", stepUp, adminH.UpdateImageStorage)
			admin.POST("/image-storage/test", adminH.TestImageStorage)
			admin.GET("/runtime-settings", runtimeSettingsH.Get)
			admin.PUT("/runtime-settings", runtimeSettingsH.Update)
			admin.GET("/audit-logs", runtimeSettingsH.ListAuditLogs)
			admin.GET("/alerts/rules", alertH.ListRules)
			admin.POST("/alerts/rules", alertH.CreateRule)
			admin.PUT("/alerts/rules/:id", alertH.UpdateRule)
			admin.DELETE("/alerts/rules/:id", alertH.DeleteRule)
			admin.GET("/alerts/events", alertH.ListEvents)
			admin.POST("/alerts/events/:id/acknowledge", alertH.AcknowledgeEvent)
			admin.GET("/alerts/silences", alertH.ListSilences)
			admin.POST("/alerts/silences", alertH.CreateSilence)
			admin.DELETE("/alerts/silences/:id", alertH.DeleteSilence)
			admin.GET("/channel-monitor/history", alertH.ChannelHistory)
			admin.GET("/backups", backupH.List)
			admin.POST("/backups", stepUp, backupH.Create)
			admin.GET("/backups/policy", backupH.Policy)
			admin.PUT("/backups/policy", backupH.UpdatePolicy)
			admin.POST("/backups/cleanup", backupH.Cleanup)
			admin.GET("/backups/:id/download", stepUp, backupH.Download)
			admin.DELETE("/backups/:id", backupH.Delete)
			admin.GET("/update/status", updateH.Status)
			admin.POST("/update/check", updateH.Check)
			admin.POST("/update/apply", updateH.Apply)
			admin.POST("/update/rollback", updateH.Rollback)
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
			admin.GET("/referral-payout/config", referralCashH.GetConfig)
			admin.PUT("/referral-payout/config", stepUp, referralCashH.UpdateConfig)
			admin.GET("/referral-payout/accounts", referralCashH.ListAccounts)
			admin.PUT("/referral-payout/accounts/:user_id", referralCashH.SaveAccount)
			admin.GET("/referral-payouts", referralCashH.ListPayouts)
			admin.POST("/referral-payouts/:id/approve", referralCashH.ApprovePayout)
			admin.POST("/referral-payouts/:id/reject", referralCashH.RejectPayout)
			admin.POST("/referral-payouts/:id/query", referralCashH.QueryPayout)
			admin.GET("/payment/config", adminPaymentH.GetConfig)
			admin.PUT("/payment/config", stepUp, adminPaymentH.UpdateConfig)
			admin.GET("/payment/providers", adminPaymentH.ListProviders)
			admin.POST("/payment/providers", stepUp, adminPaymentH.CreateProvider)
			admin.PUT("/payment/providers/:id", stepUp, adminPaymentH.UpdateProvider)
			admin.DELETE("/payment/providers/:id", adminPaymentH.DeleteProvider)
			admin.GET("/payment/orders", adminPaymentH.ListOrders)
			admin.GET("/payment/ledger", adminPaymentH.ListLedger)
			admin.POST("/payment/orders/:id/refund", adminPaymentH.ProcessRefund)
			admin.POST("/payment/orders/:id/refund/query", adminPaymentH.FinalizeRefund)
		}
	}

	mountFrontend(r)
	accountMonitor.Start()
	backupService.StartScheduler()
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
		// Unknown API paths must never fall through to index.html with HTTP
		// 200. SDKs otherwise report a misleading "empty or malformed
		// response" instead of the actionable endpoint error.
		if strings.HasPrefix(c.Request.URL.Path, "/v1/") ||
			strings.HasPrefix(c.Request.URL.Path, "/v1beta/") ||
			strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "API endpoint not found"}})
			return
		}
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
