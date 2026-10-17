package notifications

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/push"
)

type fakeDevices struct {
	tokens  []string
	removed []string
}

func (f *fakeDevices) TokensFor(context.Context, uuid.UUID) ([]string, error) { return f.tokens, nil }
func (f *fakeDevices) RemoveToken(_ context.Context, t string) error {
	f.removed = append(f.removed, t)
	return nil
}

type fakePhones struct {
	result map[string]error
	sent   []string
}

func (f *fakePhones) Send(_ context.Context, token string, _ push.Message) error {
	f.sent = append(f.sent, token)
	return f.result[token]
}

func delivery(devs *fakeDevices, ph *fakePhones) *Delivery {
	return &Delivery{Devices: devs, Phones: ph, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func send(d *Delivery) error {
	return d.Send(context.Background(), uuid.New(), "push", Message{ID: uuid.New(), Title: "t", Body: "b"})
}

func TestDeliveryPush(t *testing.T) {
	temp := errors.New("FCM down")

	// All phones get it.
	devs, ph := &fakeDevices{tokens: []string{"a", "b"}}, &fakePhones{}
	if err := send(delivery(devs, ph)); err != nil || len(ph.sent) != 2 {
		t.Fatalf("both phones: %v %v", err, ph.sent)
	}

	// Dead tokens are removed and do not fail the job.
	devs, ph = &fakeDevices{tokens: []string{"dead", "ok"}}, &fakePhones{result: map[string]error{"dead": push.ErrUnregistered}}
	if err := send(delivery(devs, ph)); err != nil || len(devs.removed) != 1 || devs.removed[0] != "dead" {
		t.Fatalf("dead token: %v removed=%v", err, devs.removed)
	}

	// Nothing got through and the cause is temporary: the queue must retry.
	devs, ph = &fakeDevices{tokens: []string{"a", "b"}}, &fakePhones{result: map[string]error{"a": temp, "b": temp}}
	if err := send(delivery(devs, ph)); !errors.Is(err, temp) {
		t.Fatalf("all failed temporarily: %v", err)
	}

	// One phone received it: do not retry (it would show the reminder twice there).
	devs, ph = &fakeDevices{tokens: []string{"a", "b"}}, &fakePhones{result: map[string]error{"b": temp}}
	if err := send(delivery(devs, ph)); err != nil {
		t.Fatalf("partial success must not retry: %v", err)
	}

	// A rejected message is not retried and does not delete the token.
	devs, ph = &fakeDevices{tokens: []string{"a"}}, &fakePhones{result: map[string]error{"a": push.ErrRejected}}
	if err := send(delivery(devs, ph)); err != nil || len(devs.removed) != 0 {
		t.Fatalf("rejected: %v removed=%v", err, devs.removed)
	}

	// No registered phone, or push not configured: quietly in-app only.
	if err := send(delivery(&fakeDevices{}, &fakePhones{})); err != nil {
		t.Fatalf("no phones: %v", err)
	}
	if err := send(&Delivery{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}); err != nil {
		t.Fatalf("push not configured: %v", err)
	}
}

func TestDeliveryIgnoresInAppAndUnknownChannels(t *testing.T) {
	ph := &fakePhones{}
	d := delivery(&fakeDevices{tokens: []string{"a"}}, ph)
	for _, ch := range []string{"in_app", "sms", ""} {
		if err := d.Send(context.Background(), uuid.New(), ch, Message{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(ph.sent) != 0 {
		t.Error("only the push channel may reach phones")
	}
}
