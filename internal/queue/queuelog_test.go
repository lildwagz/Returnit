package queue

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/returnit/returnit/internal/logging"
)

func enqueueLine(t *testing.T, task *asynq.Task, info *asynq.TaskInfo, err error) (map[string]any, string) {
	t.Helper()
	var buf bytes.Buffer
	l := logging.New(&buf, "web", slog.LevelDebug)
	logEnqueue(slog.Default(), l, task, info, err, 3*time.Millisecond)
	var m map[string]any
	if e := json.Unmarshal(buf.Bytes(), &m); e != nil {
		t.Fatalf("not JSON: %q", buf.String())
	}
	return m, buf.String()
}

func TestEnqueueLineShowsWhatWentIntoRedis(t *testing.T) {
	task := asynq.NewTask(TypeSendPush, []byte(`{"notificationId":"11111111-2222-3333-4444-555555555555"}`))
	m, _ := enqueueLine(t, task, &asynq.TaskInfo{ID: "push:1111", Queue: QueuePush}, nil)
	if m["msg"] != "queue enqueue" || m["type"] != TypeSendPush || m["result"] != "enqueued" ||
		m["queue"] != QueuePush || m["task_id"] != "push:1111" || m["level"] != "INFO" {
		t.Errorf("unexpected line: %v", m)
	}
	p, ok := m["payload"].(map[string]any)
	if !ok || p["notificationId"] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("payload not logged as JSON: %v", m["payload"])
	}
}

func TestEnqueueDuplicateAndErrorAndEmptyPayload(t *testing.T) {
	m, _ := enqueueLine(t, asynq.NewTask(TypePurgeObjects, nil), nil, asynq.ErrDuplicateTask)
	if m["result"] != "duplicate" || m["level"] != "INFO" || m["payload"] != nil {
		t.Errorf("duplicate: %v", m)
	}
	m, _ = enqueueLine(t, asynq.NewTask(TypePurgeObjects, nil), nil, errors.New("redis down"))
	if m["result"] != "error" || m["level"] != "WARN" || m["err"] != "redis down" {
		t.Errorf("error: %v", m)
	}
}

func TestLargePayloadIsNotCopiedIntoTheLog(t *testing.T) {
	big := []byte(`{"x":"` + strings.Repeat("a", 2000) + `"}`)
	m, raw := enqueueLine(t, asynq.NewTask("some:type", big), nil, nil)
	if m["payload"] != nil || m["payload_bytes"] != float64(len(big)) || strings.Contains(raw, "aaaa") {
		t.Errorf("large payload copied: %v", m)
	}
}
