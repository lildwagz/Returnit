// Package worker runs the background jobs: reminder planning and dispatch, storage retention,
// and external (push/email) delivery. Jobs arrive through Redis (Asynq); a cron-style scheduler
// in this process enqueues the periodic ones.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/queue"
	"github.com/returnit/returnit/internal/retention"
)

type Deps struct {
	Redis       queue.Config
	Concurrency int
	Reminders   *notifications.Scheduler
	Retention   *retention.Worker
	Push        *notifications.PushSender
	Accounts    *account.Service
	// Emails sends the account-deletion notices; nil when SMTP is not configured.
	Emails *account.EmailSender
	// Identities removes erased users at the identity provider; nil when it is not configured.
	Identities *account.IdentityEraser
	// LogJobs writes one INFO line per job run (LOG_REDIS=on): type, queue, attempt, duration, result.
	LogJobs bool
	// Health, if set, is told about every successful job (its readiness check needs the heartbeats).
	Health *Health
	Log    *slog.Logger
	// ShutdownTimeout is how long in-flight jobs may finish after ctx is cancelled.
	ShutdownTimeout time.Duration
	// RetryDelay overrides the queue's default retry backoff (tests only; nil keeps the default).
	RetryDelay func(attempt int) time.Duration
}

// Periodic jobs. Unique windows are slightly shorter than the interval so a slow run never
// piles up behind itself, yet every tick can run. Duplicate runs (several worker processes each
// running a scheduler) are harmless: every job is idempotent.
var periodic = []struct {
	spec   string
	task   string
	unique time.Duration
}{
	{"@every 1m", queue.TypePlanReminders, 50 * time.Second},
	{"@every 1m", queue.TypeDispatchReminders, 50 * time.Second},
	{"@every 30s", queue.TypePurgeObjects, 25 * time.Second},
	{"@every 1h", queue.TypeSweepOrphans, 55 * time.Minute},
	{"@every 15m", queue.TypePurgeAccounts, 14 * time.Minute},
	{"@every 1m", queue.TypeSendEmails, 50 * time.Second},
	{"@every 1m", queue.TypeEraseIdentities, 50 * time.Second},
}

// Run starts the job server and the periodic scheduler and blocks until ctx is cancelled.
func Run(ctx context.Context, d Deps) error {
	opt := d.Redis.RedisOpt()
	cfg := asynq.Config{
		Concurrency:     d.Concurrency,
		Queues:          queue.QueueWeights,
		ShutdownTimeout: d.ShutdownTimeout,
		Logger:          slogAdapter{d.Log},
		// Called for every failed attempt, including recovered panics. Payloads are never logged.
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			retried, _ := asynq.GetRetryCount(ctx)
			maxRetry, _ := asynq.GetMaxRetry(ctx)
			taskID, _ := asynq.GetTaskID(ctx)
			d.Log.Warn("job failed", "type", t.Type(), "task_id", taskID, "attempt", retried+1, "max_retries", maxRetry, "err", err)
		}),
	}
	if d.RetryDelay != nil {
		cfg.RetryDelayFunc = func(n int, _ error, _ *asynq.Task) time.Duration { return d.RetryDelay(n) }
	}
	srv := asynq.NewServer(opt, cfg)
	mux := asynq.NewServeMux()
	// One debug line per successful job (periodic jobs run every few seconds to minutes, so this
	// is off at the default info level). Failures are logged by the ErrorHandler above.
	mux.Use(func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
			start := time.Now()
			err := next.ProcessTask(ctx, t)
			if d.LogJobs {
				taskID, _ := asynq.GetTaskID(ctx)
				queueName, _ := asynq.GetQueueName(ctx)
				retried, _ := asynq.GetRetryCount(ctx)
				result := "ok"
				if err != nil {
					result = "failed" // the reason is logged once by the error handler above
				}
				d.Log.Info("job", "type", t.Type(), "queue", queueName, "task_id", taskID, "attempt", retried+1,
					"result", result, "duration_ms", time.Since(start).Milliseconds())
			}
			if err == nil {
				if d.Health != nil {
					d.Health.Beat(t.Type())
				}
				taskID, _ := asynq.GetTaskID(ctx)
				d.Log.Debug("job done", "type", t.Type(), "task_id", taskID, "duration_ms", time.Since(start).Milliseconds())
			}
			return err
		})
	})
	h := handlers{d}
	mux.HandleFunc(queue.TypePlanReminders, h.plan)
	mux.HandleFunc(queue.TypeDispatchReminders, h.dispatch)
	mux.HandleFunc(queue.TypePurgeObjects, h.purge)
	mux.HandleFunc(queue.TypeSweepOrphans, h.sweep)
	mux.HandleFunc(queue.TypePurgeAccounts, h.purgeAccounts)
	mux.HandleFunc(queue.TypeSendEmails, h.emails)
	mux.HandleFunc(queue.TypeEraseIdentities, h.identities)
	mux.HandleFunc(queue.TypeSendPush, h.push)

	sched := asynq.NewScheduler(opt, &asynq.SchedulerOpts{Location: time.UTC, Logger: slogAdapter{d.Log}})
	for _, p := range periodic {
		if _, err := sched.Register(p.spec, asynq.NewTask(p.task, nil),
			asynq.Queue(queue.QueueDefault), asynq.Unique(p.unique), asynq.MaxRetry(2), asynq.Timeout(5*time.Minute)); err != nil {
			return fmt.Errorf("worker: register %s: %w", p.task, err)
		}
	}

	if err := srv.Start(mux); err != nil {
		return fmt.Errorf("worker: start server: %w", err)
	}
	if err := sched.Start(); err != nil {
		srv.Shutdown()
		return fmt.Errorf("worker: start scheduler: %w", err)
	}
	d.Log.Info("worker started", "concurrency", d.Concurrency)

	<-ctx.Done()
	d.Log.Info("worker shutting down")
	sched.Shutdown()
	srv.Shutdown() // waits for in-flight jobs up to ShutdownTimeout
	return nil
}

type handlers struct{ d Deps }

func (h handlers) plan(ctx context.Context, _ *asynq.Task) error {
	n, err := h.d.Reminders.Plan(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		h.d.Log.Info("reminders planned", "count", n)
	}
	return nil
}

func (h handlers) dispatch(ctx context.Context, _ *asynq.Task) error {
	st, err := h.d.Reminders.Dispatch(ctx)
	if err != nil {
		return err
	}
	if st.Sent+st.Cancelled+st.Deferred > 0 {
		h.d.Log.Info("reminders dispatched", "sent", st.Sent, "cancelled", st.Cancelled, "deferred", st.Deferred)
	}
	return nil
}

func (h handlers) purge(ctx context.Context, _ *asynq.Task) error {
	n, err := h.d.Retention.PurgeQueued(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		h.d.Log.Info("receipt objects purged", "count", n)
	}
	return nil
}

// purgeAccounts erases accounts whose grace period has ended, then immediately drains the file
// deletion queue the erasure filled (the trigger queued every receipt file).
func (h handlers) purgeAccounts(ctx context.Context, _ *asynq.Task) error {
	n, err := h.d.Accounts.PurgeDue(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		h.d.Log.Info("accounts erased after grace period", "count", n)
		if _, err := h.d.Retention.PurgeQueued(ctx); err != nil {
			return err
		}
	}
	return nil
}

// emails sends the account-deletion notices. Without SMTP configured they simply stay queued.
func (h handlers) emails(ctx context.Context, _ *asynq.Task) error {
	if h.d.Emails == nil {
		return nil
	}
	n, err := h.d.Emails.SendDue(ctx)
	if n > 0 {
		h.d.Log.Info("account e-mails sent", "count", n)
	}
	return err
}

// identities deletes the login identities of erased accounts. Without a provider configured they
// stay queued.
func (h handlers) identities(ctx context.Context, _ *asynq.Task) error {
	if h.d.Identities == nil {
		return nil
	}
	n, err := h.d.Identities.EraseDue(ctx)
	if n > 0 {
		h.d.Log.Info("identities deleted at the identity provider", "count", n)
	}
	return err
}

func (h handlers) sweep(ctx context.Context, _ *asynq.Task) error {
	n, err := h.d.Retention.SweepOrphans(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		h.d.Log.Info("orphaned receipt objects removed", "count", n)
	}
	return nil
}

// push performs external delivery. Queue retries (with exponential backoff, up to MaxRetry) cover
// transient provider errors; permanent errors and vanished notifications are not retried.
func (h handlers) push(ctx context.Context, t *asynq.Task) error {
	id, err := queue.ParsePush(t)
	if err != nil {
		return err
	}
	err = h.d.Push.Send(ctx, id)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, notifications.ErrPermanent), errors.Is(err, notifications.ErrGone):
		h.d.Log.Error("push delivery abandoned", "notification_id", id, "err", err)
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	default:
		return err // includes ErrNotReady: retried by the queue
	}
}

// slogAdapter routes Asynq's logger into slog.
type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Debug(args ...any) {}
func (a slogAdapter) Info(args ...any)  {}
func (a slogAdapter) Warn(args ...any)  { a.l.Warn(fmt.Sprint(args...)) }
func (a slogAdapter) Error(args ...any) { a.l.Error(fmt.Sprint(args...)) }
func (a slogAdapter) Fatal(args ...any) { a.l.Error(fmt.Sprint(args...)) }
