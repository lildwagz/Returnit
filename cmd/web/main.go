// Command web is the HTTP API process. It serves requests only; background work (reminders,
// retention, push delivery) runs in cmd/worker and is reached through Redis (internal/queue).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed zone data so Asia/Jakarta (WIB) resolves regardless of the runtime image

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/auth"
	"github.com/returnit/returnit/internal/config"
	"github.com/returnit/returnit/internal/database"
	"github.com/returnit/returnit/internal/devices"
	"github.com/returnit/returnit/internal/httpapi"
	"github.com/returnit/returnit/internal/logging"
	"github.com/returnit/returnit/internal/malware"
	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/probe"
	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/queue"
	"github.com/returnit/returnit/internal/ratelimit"
	"github.com/returnit/returnit/internal/receipts"
	"github.com/returnit/returnit/internal/reminders"
	"github.com/returnit/returnit/internal/storage"
	"github.com/returnit/returnit/internal/users"
)

func main() {
	log := logging.NewFromEnv("web")
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	healthcheck := flag.Bool("healthcheck", false, "probe the local server's /health/live and exit 0 (healthy) or 1")
	flag.Parse()
	if *healthcheck {
		addr := os.Getenv("HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		code, msg := probe.Run(probe.URL(addr, "/health/live"))
		fmt.Println(msg)
		os.Exit(code)
	}

	cfg, err := config.LoadWeb()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	verifier, err := auth.NewOIDCVia(ctx, cfg.OIDCIssuer, cfg.OIDCDiscoveryURL, cfg.OIDCAudience)
	if err != nil {
		return err
	}

	store, err := storage.NewS3(ctx, cfg.S3)
	if err != nil {
		return err
	}
	var scanner malware.Scanner = malware.Disabled{}
	if cfg.MalwareScan == "on" {
		scanner = malware.NewClamAV(cfg.ClamAVAddr)
	} else {
		log.Warn("MALWARE_SCAN=off: uploaded receipts are NOT scanned; never use this in production")
	}

	// Redis is a best-effort transport for the web process: requests never fail because it is
	// down, since Postgres holds the truth and the worker's periodic jobs catch up.
	if err := queue.Ping(cfg.Redis); err != nil {
		log.Warn("redis unreachable at startup; background nudges will be delayed until it is back", "err", err)
	}
	jobs := queue.NewClient(cfg.Redis)
	defer jobs.Close()

	// Shared limits live in Redis; if it is unreachable each instance falls back to its own
	// in-memory limits rather than disabling protection.
	redisLimiter := ratelimit.NewRedis(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	defer redisLimiter.Close()
	limiter := &ratelimit.Fallback{Primary: redisLimiter, Secondary: ratelimit.NewMemory(), Log: log}

	receiptSvc := receipts.NewService(receipts.NewPGRepo(pool), store, scanner, jobs, cfg.MaxReceiptBytes, log)
	router := httpapi.NewRouter(httpapi.Deps{
		DB:               pool,
		Verifier:         verifier,
		Users:            users.NewStore(pool),
		Purchases:        purchases.NewService(purchases.NewPGRepo(pool), receiptSvc),
		Receipts:         receiptSvc,
		Reminders:        reminders.NewService(reminders.NewPGRepo(pool)),
		Notifications:    notifications.NewService(pool),
		Account:          account.NewService(pool, cfg.AccountDeletionGrace),
		Devices:          devices.NewService(pool),
		Logger:           log,
		Limiter:          limiter,
		Limits:           cfg.RateLimits,
		TrustedProxies:   cfg.TrustedProxies,
		RequestTimeout:   cfg.RequestTimeout,
		FreshLoginMaxAge: cfg.FreshLoginMaxAge,
		LogBodies:        httpapi.BodyLog{Enabled: cfg.LogRequestBody, MaxBytes: cfg.LogRequestBodyMax},
	})
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: router, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		if cfg.LogRequestBody {
			log.Warn("LOG_REQUEST_BODY is on: request and response bodies (personal data) are written to the log; use it only for development or short debugging")
		}
		log.Info("web listening", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(sctx)
	}
	return nil
}
