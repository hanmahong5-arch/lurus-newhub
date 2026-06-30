package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/handler/router"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/hub"
	"github.com/LurusTech/lurus-hub/internal/app/openrouter_pool"
	openrouter_sync "github.com/LurusTech/lurus-hub/internal/app/openrouter_sync"
	"github.com/LurusTech/lurus-hub/internal/lifecycle"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/config"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	hubnats "github.com/LurusTech/lurus-hub/internal/pkg/nats"
	"github.com/LurusTech/lurus-hub/internal/pkg/search"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/tracing"
	"github.com/LurusTech/lurus-hub/web"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	sessionredis "github.com/gin-contrib/sessions/redis"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"golang.org/x/sync/errgroup"

	_ "net/http/pprof" // #nosec G108 — pprof is exposed only when ENABLE_PPROF=true (gated in router); disabled in production.
)

var buildFS = web.BuildFS
var indexPage = web.IndexPage

func main() {
	startTime := time.Now()

	// Create root context with signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, startTime); err != nil && !errors.Is(err, context.Canceled) {
		common.FatalLog("server error: " + err.Error())
		os.Exit(1)
	}

	common.SysLog("server shutdown complete")
}

func run(ctx context.Context, startTime time.Time) error {
	if err := InitResources(ctx); err != nil {
		return fmt.Errorf("failed to initialize resources: %w", err)
	}

	common.SysLog("New API " + common.Version + " started")
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}

	// Ensure database is closed on shutdown
	defer func() {
		if err := repo.CloseDB(); err != nil {
			common.SysError("failed to close database: " + err.Error())
		}
	}()

	// Ensure tracing is shutdown gracefully
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tracing.Shutdown(shutdownCtx); err != nil {
			common.SysError("failed to shutdown tracing: " + err.Error())
		}
	}()

	// Use errgroup for managing background goroutines with context cancellation
	g, ctx := errgroup.WithContext(ctx)

	// HA leader election: master-capable nodes contend for a single DB lease.
	// The winner's common.IsLeader() flips true, and every master-only
	// background task below gates its per-tick work on it — so exactly one
	// replica runs them and a dead leader is replaced within the lease TTL.
	// Slaves (NODE_TYPE=slave) do not participate and are never leader.
	if common.IsMasterNode {
		leaderManager := lifecycle.NewLeaderManager()
		g.Go(func() error {
			_ = leaderManager.Run(ctx)
			return nil
		})
	}

	if common.RedisEnabled {
		common.MemoryCacheEnabled = true
	}
	if common.MemoryCacheEnabled {
		common.SysLog("memory cache enabled")
		common.SysLog(fmt.Sprintf("sync frequency: %d seconds", common.SyncFrequency))

		// Initialize channel cache with panic recovery
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysLog(fmt.Sprintf("InitChannelCache panic: %v, retrying once", r))
					if _, _, fixErr := repo.FixAbility(); fixErr != nil {
						common.SysError(fmt.Sprintf("InitChannelCache failed: %s", fixErr.Error()))
					}
				}
			}()
			repo.InitChannelCache()
		}()

		// Background task: sync channel cache
		g.Go(func() error {
			repo.SyncChannelCacheWithContext(ctx, common.SyncFrequency)
			return nil
		})
	}

	// Background task: sync options (hot reload config)
	g.Go(func() error {
		repo.SyncOptionsWithContext(ctx, common.SyncFrequency)
		return nil
	})

	// Background task: update quota dashboard data
	g.Go(func() error {
		repo.UpdateQuotaDataWithContext(ctx)
		return nil
	})

	// Hub data processing: channel scoring + usage aggregation
	hub.Init(func(flushCtx context.Context, buckets []hub.UsageBucket) error {
		// Persist aggregated usage buckets to quota_data table
		for _, b := range buckets {
			repo.LogQuotaData(0, b.TenantID, b.ModelName, int(b.QuotaConsumed),
				b.WindowStart.Unix(), int(b.InputTokens+b.OutputTokens))
		}
		return nil
	})
	g.Go(func() error {
		hub.Get().RunBackgroundTasks(ctx)
		return nil
	})

	if os.Getenv("CHANNEL_UPDATE_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
		if err != nil {
			return fmt.Errorf("failed to parse CHANNEL_UPDATE_FREQUENCY: %w", err)
		}
		g.Go(func() error {
			handler.AutomaticallyUpdateChannelsWithContext(ctx, frequency)
			return nil
		})
	}

	if os.Getenv("MODEL_SYNC_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("MODEL_SYNC_FREQUENCY"))
		if err != nil {
			return fmt.Errorf("failed to parse MODEL_SYNC_FREQUENCY: %w", err)
		}
		g.Go(func() error {
			handler.AutoSyncChannelModelsWithContext(ctx, frequency)
			return nil
		})
	}

	// OpenRouter free-model sync — master-only. Disabled when env var is empty.
	if os.Getenv("OPENROUTER_FREE_SYNC_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("OPENROUTER_FREE_SYNC_FREQUENCY"))
		if err != nil {
			return fmt.Errorf("failed to parse OPENROUTER_FREE_SYNC_FREQUENCY: %w", err)
		}
		g.Go(func() error {
			openrouter_sync.AutoSyncWithContext(ctx, frequency)
			return nil
		})
	}
	// Hourly aggregator runs whenever OpenRouter sync is configured (any sync job present
	// will benefit from ranking data). Kept independent of OPENROUTER_FREE_SYNC_FREQUENCY
	// so manual triggers still get fresh ranks even when scheduling is disabled.
	if common.IsMasterNode {
		g.Go(func() error {
			openrouter_sync.AutoAggregateWithContext(ctx)
			return nil
		})
		// OpenRouter pool reaper: re-enables keys whose rate-limit cooldown has expired.
		// Runs even when OPENROUTER_FREE_SYNC_FREQUENCY is unset — pool cooldown is
		// triggered by live relay traffic, not by the sync feature.
		g.Go(func() error {
			openrouter_pool.AutoReapWithContext(ctx)
			return nil
		})
	}

	// Background task: automatically test channels
	g.Go(func() error {
		handler.AutomaticallyTestChannelsWithContext(ctx)
		return nil
	})

	if common.IsMasterNode && constant.UpdateTask {
		g.Go(func() error {
			handler.UpdateMidjourneyTaskBulkWithContext(ctx)
			return nil
		})
		g.Go(func() error {
			handler.UpdateTaskBulkWithContext(ctx)
			return nil
		})
	}

	// Background task: poll platform billing-config endpoint every 30s so the
	// unified-billing toggle can be flipped from the platform console at runtime
	// without a pod restart.  No-op when IDENTITY_SERVICE_URL is unset.
	common.StartBillingConfigPoller(ctx)

	// Background task: billing outbox processor (5s ticker)
	if common.BillingUnifiedEnabled() {
		g.Go(func() error {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					_ = app.ProcessBillingOutbox(ctx)
				}
			}
		})
	}

	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		common.BatchUpdateEnabled = true
		common.SysLog("batch update enabled with interval " + strconv.Itoa(common.BatchUpdateInterval) + "s")
		repo.InitBatchUpdater()
	}

	// Start daily quota reset cron job with context for graceful shutdown
	if os.Getenv("DAILY_QUOTA_ENABLED") != "false" {
		repo.StartDailyQuotaResetCronWithContext(ctx)
	}

	// Phase E3: audit retention cleanup. Master-only to avoid duplicate
	// deletes when scaled out; the operation is idempotent but firing it
	// once per node would inflate DB write amplification.
	if common.IsMasterNode {
		lifecycle.StartAuditCleanupWithContext(ctx)
		// Phase H1.4: automatic token rotation. Leader-gated internally so only
		// one replica rotates each due token; started on master-capable nodes.
		lifecycle.StartSecretRotationWithContext(ctx)
		// PIPL §47 erasure cascade executor. Leader-gated internally; the
		// cascade is idempotent + crash-resumable via the per-request step cursor.
		lifecycle.StartPrivacyErasureWithContext(ctx)
	}

	// pprof server
	if os.Getenv("ENABLE_PPROF") == "true" {
		pprofServer := &http.Server{Addr: "127.0.0.1:8005", Handler: nil}
		g.Go(func() error {
			common.SysLog("pprof enabled on :8005")
			if err := pprofServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("pprof server error: %w", err)
			}
			return nil
		})
		g.Go(func() error {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return pprofServer.Shutdown(shutdownCtx)
		})
		g.Go(func() error {
			common.MonitorWithContext(ctx)
			return nil
		})
	}

	if err := common.StartPyroScope(); err != nil {
		common.SysError(fmt.Sprintf("start pyroscope error: %v", err))
	}

	// Initialize Gin HTTP server
	engine := gin.New()
	engine.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		common.SysLog(fmt.Sprintf("panic detected: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Panic detected, error: %v. Please submit an issue.", err),
				"type":    "new_api_panic",
			},
		})
	}))
	engine.Use(middleware.RequestId())
	middleware.SetUpLogger(engine)

	// Initialize session store (Redis if available, cookie fallback)
	sessionSecure := os.Getenv("GIN_MODE") == "release"
	if envSecure := os.Getenv("SESSION_SECURE"); envSecure != "" {
		sessionSecure = envSecure == "true"
	}
	// Default to ".lurus.cn" so the lurus.cn deployments share session cookies
	// across subdomains. Standalone deployments under a single host (especially
	// when accessed via IP, where browsers reject mismatched Domain attributes
	// and silently drop the cookie) should set SESSION_COOKIE_DOMAIN="" to opt
	// into a host-only cookie.
	cookieDomain := ".lurus.cn"
	if d, ok := os.LookupEnv("SESSION_COOKIE_DOMAIN"); ok {
		cookieDomain = d
	}
	sessionOpts := sessions.Options{
		Path:     "/",
		MaxAge:   7776000, // 90 days
		HttpOnly: true,
		Secure:   sessionSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   cookieDomain,
	}
	var store sessions.Store
	if redisURL := os.Getenv("REDIS_CONN_STRING"); redisURL != "" {
		// Parse redis://host:port format to extract address and password
		redisAddr := "127.0.0.1:6379"
		redisPassword := ""
		if strings.HasPrefix(redisURL, "redis://") {
			parsed := strings.TrimPrefix(redisURL, "redis://")
			// Handle redis://password@host:port or redis://host:port
			if atIdx := strings.LastIndex(parsed, "@"); atIdx >= 0 {
				redisPassword = parsed[:atIdx]
				redisAddr = parsed[atIdx+1:]
			} else {
				redisAddr = parsed
			}
			// Remove trailing path if present
			if slashIdx := strings.Index(redisAddr, "/"); slashIdx >= 0 {
				redisAddr = redisAddr[:slashIdx]
			}
		}
		var err error
		store, err = sessionredis.NewStore(10, "tcp", redisAddr, "", redisPassword, []byte(common.SessionSecret))
		if err != nil {
			common.SysLog("Failed to create Redis session store, falling back to cookie: " + err.Error())
			store = cookie.NewStore([]byte(common.SessionSecret))
		} else {
			common.SysLog("Session store: Redis (" + redisAddr + ")")
		}
	} else {
		store = cookie.NewStore([]byte(common.SessionSecret))
		common.SysLog("Session store: cookie")
	}
	store.Options(sessionOpts)
	// Propagate MaxAge to securecookie codecs — gin-contrib/sessions Options()
	// only sets cookie MaxAge but leaves codec timestamp validation at the default
	// 30 days, causing "securecookie: expired timestamp" for sessions between
	// 30-90 days old. SetMaxAge updates both cookie options AND codec MaxAge.
	type maxAger interface{ SetMaxAge(int) }
	if ma, ok := store.(maxAger); ok {
		ma.SetMaxAge(sessionOpts.MaxAge)
	}
	engine.Use(sessions.Sessions("session", store))

	InjectUmamiAnalytics()
	InjectGoogleAnalytics()

	// Initialize release service
	handler.InitReleaseService()

	// Start tool version background worker (polls npm / GitHub, caches in Redis)
	if common.RedisEnabled && common.RDB != nil {
		handler.StartToolVersionWorker(ctx, common.RDB)
	}

	// Setup routes
	router.SetRouter(engine, buildFS, indexPage)

	port := os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}

	// Create http.Server for graceful shutdown
	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: engine,
	}

	// Start HTTP server
	g.Go(func() error {
		common.LogStartupSuccess(startTime, port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server error: %w", err)
		}
		return nil
	})

	// Graceful shutdown handler
	g.Go(func() error {
		<-ctx.Done()
		common.SysLog("shutdown signal received, initiating graceful shutdown...")

		// Allow 30 seconds for graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("HTTP server shutdown error: %w", err)
		}
		common.SysLog("HTTP server shutdown complete")
		return nil
	})

	// Wait for all goroutines to complete
	return g.Wait()
}

func InjectUmamiAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("UMAMI_WEBSITE_ID") != "" {
		umamiSiteID := os.Getenv("UMAMI_WEBSITE_ID")
		umamiScriptURL := os.Getenv("UMAMI_SCRIPT_URL")
		if umamiScriptURL == "" {
			umamiScriptURL = "https://analytics.umami.is/script.js"
		}
		analyticsInjectBuilder.WriteString("<script defer src=\"")
		analyticsInjectBuilder.WriteString(umamiScriptURL)
		analyticsInjectBuilder.WriteString("\" data-website-id=\"")
		analyticsInjectBuilder.WriteString(umamiSiteID)
		analyticsInjectBuilder.WriteString("\"></script>")
	}
	analyticsInjectBuilder.WriteString("<!--Umami QuantumNous-->\n")
	analyticsInject := analyticsInjectBuilder.String()
	indexPage = bytes.ReplaceAll(indexPage, []byte("<!--umami-->\n"), []byte(analyticsInject))
}

func InjectGoogleAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("GOOGLE_ANALYTICS_ID") != "" {
		gaID := os.Getenv("GOOGLE_ANALYTICS_ID")
		// Google Analytics 4 (gtag.js)
		analyticsInjectBuilder.WriteString("<script async src=\"https://www.googletagmanager.com/gtag/js?id=")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("\"></script>")
		analyticsInjectBuilder.WriteString("<script>")
		analyticsInjectBuilder.WriteString("window.dataLayer = window.dataLayer || [];")
		analyticsInjectBuilder.WriteString("function gtag(){dataLayer.push(arguments);}")
		analyticsInjectBuilder.WriteString("gtag('js', new Date());")
		analyticsInjectBuilder.WriteString("gtag('config', '")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("');")
		analyticsInjectBuilder.WriteString("</script>")
	}
	analyticsInjectBuilder.WriteString("<!--Google Analytics QuantumNous-->\n")
	analyticsInject := analyticsInjectBuilder.String()
	indexPage = bytes.ReplaceAll(indexPage, []byte("<!--Google Analytics-->\n"), []byte(analyticsInject))
}

func InitResources(ctx context.Context) error {
	// Initialize resources here if needed
	// This is a placeholder function for future resource initialization
	err := godotenv.Load(".env")
	if err != nil {
		if common.DebugEnabled {
			common.SysLog("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables.")
		}
	}

	// 加载环境变量
	common.InitEnv()

	// Initialize structured logging from env vars (LOG_FORMAT, LOG_LEVEL)
	common.InitSlog(common.SlogConfigFromEnv())

	logger.SetupLogger()

	// Load centralized config and log effective values
	common.SysLog(config.Get().PrintEffective())

	// Initialize model settings
	ratio_setting.InitRatioSettings()

	app.InitHttpClient()

	app.InitTokenEncoders()

	// Initialize SQL Database
	err = repo.InitDB()
	if err != nil {
		common.FatalLog("failed to initialize database: " + err.Error())
		return err
	}

	repo.CheckSetup()

	// Initialize audit event writer for governance system
	governance.SetAuditWriter(&repo.AuditEventRepo{})

	// Initialize options, should after repo.InitDB()
	repo.InitOptionMap()

	// 初始化模型
	repo.GetPricing()

	// Initialize SQL Database
	err = repo.InitLogDB()
	if err != nil {
		return err
	}

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		return err
	}

	// Initialize Meilisearch
	// 初始化 Meilisearch
	err = search.InitMeilisearch()
	if err != nil {
		common.SysError(fmt.Sprintf("Failed to initialize Meilisearch: %v", err))
		// Don't return error - Meilisearch is optional
		// 不返回错误 - Meilisearch 是可选的
	} else if search.IsEnabled() {
		// Initialize search sync mechanism
		// 初始化搜索同步机制
		err = search.InitSyncWithContext(ctx)
		if err != nil {
			common.SysError(fmt.Sprintf("Failed to initialize Meilisearch sync: %v", err))
		}
	}

	// Initialize billing outbox (pre-auth settlement retry queue)
	if common.BillingUnifiedEnabled() {
		if err := app.InitBillingOutbox(repo.DB); err != nil {
			common.SysError(fmt.Sprintf("Failed to initialize billing outbox: %v", err))
		} else {
			common.SysLog("billing unified mode enabled, outbox initialized")
		}
	}

	// Initialize zita SDK client (Layer B/C of ADR-0011 — replaces
	// direct IdP coupling per ADR-0005). Optional: nil ZitaClient
	// when env not set, legacy OIDC path remains as fallback.
	common.InitZitaClient()

	// Initialize OIDC authentication (multi-tenant OAuth)
	// 初始化 OIDC 认证（多租户 OAuth）
	err = middleware.InitOIDCAuth()
	if err != nil {
		common.SysError(fmt.Sprintf("Failed to initialize OIDC authentication: %v", err))
		// Do not return error - OIDC is optional and can be enabled later
		// 不返回错误 - OIDC 是可选的，可以稍后启用
	}

	// Initialize OpenTelemetry tracing
	// 初始化 OpenTelemetry 追踪
	tracingCfg := tracing.LoadConfigFromEnv()
	if err := tracing.Init(ctx, tracingCfg); err != nil {
		common.SysError(fmt.Sprintf("Failed to initialize tracing: %v", err))
		// Don't return error - tracing is optional
		// 不返回错误 - 追踪是可选的
	}

	// Initialize NATS publisher for LLM_QUOTA threshold events.
	// Optional — Init is no-op when LLM_QUOTA_NATS_ENABLED=false.
	if hubnats.Enabled() {
		if err := hubnats.Init(); err != nil {
			common.SysError(fmt.Sprintf("Failed to initialize NATS quota publisher: %v", err))
			// Non-fatal: quota threshold events skipped, main path unaffected
		} else {
			common.SysLog(fmt.Sprintf("NATS quota publisher initialized, url=%s stream=%s",
				os.Getenv("NATS_URL"), os.Getenv("LLM_QUOTA_NATS_STREAM")))
		}
	}

	return nil
}
