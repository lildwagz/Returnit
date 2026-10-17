// Command worker runs background jobs: reminder planning/dispatch, receipt-file retention and
// push/email delivery. It receives and schedules work through Redis (internal/queue) and never
// serves HTTP; cmd/web is the API process.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // embed zone data so Asia/Jakarta (WIB) resolves regardless of the runtime image

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/config"
	"github.com/returnit/returnit/internal/database"
	"github.com/returnit/returnit/internal/devices"
	"github.com/returnit/returnit/internal/identity"
	"github.com/returnit/returnit/internal/logging"
	"github.com/returnit/returnit/internal/mailer"
	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/probe"
	"github.com/returnit/returnit/internal/push"
	"github.com/returnit/returnit/internal/queue"
	"github.com/returnit/returnit/internal/retention"
	"github.com/returnit/returnit/internal/storage"
	"github.com/returnit/returnit/internal/worker"
)

func main() {
	log := logging.NewFromEnv("worker")
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	healthcheck := flag.Bool("healthcheck", false, "probe the local worker's /health/ready and exit 0 (healthy) or 1")
	flag.Parse()
	if *healthcheck {
		code, msg := probe.Run(probe.URL(envOr("WORKER_HEALTH_ADDR", ":8081"), "/health/ready"))
		fmt.Println(msg)
		os.Exit(code)
	}

	cfg, err := config.LoadWorker()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The worker cannot do anything useful without Redis, so fail fast (unlike the web process).
	if err := queue.Ping(cfg.Redis); err != nil {
		return err
	}
	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store, err := storage.NewS3(ctx, cfg.S3)
	if err != nil {
		return err
	}

	jobs := queue.NewClient(cfg.Redis)
	defer jobs.Close()

	health := worker.NewHealth(pool, cfg.Redis)
	go func() {
		if err := health.Serve(ctx, cfg.HealthAddr); err != nil {
			log.Error("health server stopped", "err", err)
		}
	}()
	log.Info("worker health and metrics", "addr", cfg.HealthAddr)

	var emails *account.EmailSender
	if cfg.SMTP.Addr != "" {
		emails = &account.EmailSender{Pool: pool, Log: log, Mailer: &mailer.SMTP{
			Addr: cfg.SMTP.Addr, From: cfg.SMTP.From, Username: cfg.SMTP.Username, Password: cfg.SMTP.Password, TLS: cfg.SMTP.TLS}}
	} else {
		log.Warn("SMTP_ADDR is not set: account-deletion e-mails stay queued until e-mail is configured")
	}

	devs := devices.NewService(pool)
	delivery := &notifications.Delivery{Pool: pool, Devices: devs, Log: log}
	if emails != nil {
		delivery.Mailer = emails.Mailer
	}
	if cfg.FCMProjectID != "" {
		key, err := os.ReadFile(cfg.FCMCredentialsFile)
		if err != nil {
			return fmt.Errorf("read FCM_CREDENTIALS_FILE: %w", err)
		}
		fcm, err := push.NewFCM(ctx, cfg.FCMProjectID, key)
		if err != nil {
			return err
		}
		delivery.Phones = fcm
	} else {
		log.Warn("FCM_PROJECT_ID is not set: push reminders are not sent to phones (they still appear in the app)")
	}

	var identities *account.IdentityEraser
	if k := cfg.Keycloak; k.BaseURL != "" {
		identities = &account.IdentityEraser{Pool: pool, Log: log, Provider: &identity.Keycloak{
			BaseURL: k.BaseURL, Realm: k.Realm, ClientID: k.ClientID, ClientSecret: k.ClientSecret}}
	} else {
		log.Warn("KEYCLOAK_URL is not set: erased users stay at the identity provider until it is configured")
	}

	return worker.Run(ctx, worker.Deps{
		Identities:      identities,
		Emails:          emails,
		Health:          health,
		Redis:           cfg.Redis,
		Concurrency:     cfg.Concurrency,
		Reminders:       &notifications.Scheduler{Pool: pool, Pushes: jobs, Log: log},
		Retention:       &retention.Worker{Pool: pool, Store: store, Log: log, OrphanGrace: cfg.OrphanGrace},
		Accounts:        account.NewService(pool, 0), // grace is stored per row; only PurgeDue is used
		Push:            &notifications.PushSender{Pool: pool, Pusher: delivery},
		Log:             log,
		ShutdownTimeout: cfg.ShutdownTimeout,
	})
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
