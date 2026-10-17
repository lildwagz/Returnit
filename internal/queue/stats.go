package queue

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/hibiken/asynq"
)

// QueueStat is a point-in-time view of one queue, for metrics.
type QueueStat struct {
	Queue    string
	Pending  int
	Active   int
	Retry    int           // failed attempts waiting to be retried
	Archived int           // gave up after exhausting retries: needs a human
	Latency  time.Duration // how long the oldest pending task has been waiting
}

// Stats reports every queue the worker serves. A queue that does not exist yet (nothing was ever
// enqueued) is reported as empty rather than as an error.
func Stats(cfg Config) ([]QueueStat, error) {
	in := asynq.NewInspector(cfg.probeOpt())
	defer in.Close()

	names := make([]string, 0, len(QueueWeights))
	for q := range QueueWeights {
		names = append(names, q)
	}
	sort.Strings(names)

	out := make([]QueueStat, 0, len(names))
	for _, q := range names {
		info, err := in.GetQueueInfo(q)
		if err != nil {
			if errors.Is(err, asynq.ErrQueueNotFound) {
				out = append(out, QueueStat{Queue: q})
				continue
			}
			return nil, fmt.Errorf("queue: stats for %s: %w", q, err)
		}
		out = append(out, QueueStat{Queue: q, Pending: info.Pending, Active: info.Active,
			Retry: info.Retry, Archived: info.Archived, Latency: info.Latency})
	}
	return out, nil
}
