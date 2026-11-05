package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/buckets"
	"github.com/returnit/returnit/internal/ratelimit"
)

// fakeBuckets answers every call with the error in err (nil means success) and records the last confirm name and token.
type fakeBuckets struct {
	err     error
	confirm string
	token   string
}

func (f *fakeBuckets) List(context.Context, uuid.UUID) ([]buckets.Bucket, error) {
	return []buckets.Bucket{{ID: uuid.New(), Name: "Personal", Personal: true, Role: "owner"}}, f.err
}
func (f *fakeBuckets) Get(context.Context, uuid.UUID, uuid.UUID) (buckets.Bucket, error) {
	return buckets.Bucket{Name: "Home"}, f.err
}
func (f *fakeBuckets) Create(_ context.Context, _ uuid.UUID, name string) (buckets.Bucket, error) {
	return buckets.Bucket{Name: name, Role: "owner"}, f.err
}
func (f *fakeBuckets) Rename(_ context.Context, _, _ uuid.UUID, name string) (buckets.Bucket, error) {
	return buckets.Bucket{Name: name}, f.err
}
func (f *fakeBuckets) Delete(_ context.Context, _, _ uuid.UUID, confirm string) error {
	f.confirm = confirm
	return f.err
}
func (f *fakeBuckets) Members(context.Context, uuid.UUID, uuid.UUID) ([]buckets.Member, error) {
	return []buckets.Member{{ID: uuid.New(), Name: "Alice", Role: "owner", You: true}}, f.err
}
func (f *fakeBuckets) RemoveMember(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return f.err
}
func (f *fakeBuckets) TransferOwner(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return f.err
}
func (f *fakeBuckets) SetNotify(_ context.Context, _, _ uuid.UUID, notify bool) (buckets.Bucket, error) {
	return buckets.Bucket{Notify: notify}, f.err
}
func (f *fakeBuckets) CreateInvite(context.Context, uuid.UUID, uuid.UUID) (buckets.Invite, error) {
	return buckets.Invite{Token: "ABCDEFGH", ExpiresAt: time.Now().Add(time.Hour)}, f.err
}
func (f *fakeBuckets) RevokeInvite(context.Context, uuid.UUID, uuid.UUID) error { return f.err }
func (f *fakeBuckets) Preview(_ context.Context, token string) (buckets.InvitePreview, error) {
	f.token = token
	return buckets.InvitePreview{BucketName: "Home", MemberCount: 2}, f.err
}
func (f *fakeBuckets) Join(_ context.Context, _ uuid.UUID, token string) (buckets.Bucket, error) {
	f.token = token
	return buckets.Bucket{Name: "Home", Role: "member"}, f.err
}

func TestBucketRoutes(t *testing.T) {
	alice := uuid.New()
	fb := &fakeBuckets{}
	h := newRouterWith(func(d *Deps) {
		d.Users = &fakeUsers{ids: map[string]uuid.UUID{"alice": alice}}
		d.Buckets = fb
	})
	id := uuid.NewString()

	if w := do(h, "GET", "/api/v1/buckets", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if w := do(h, "GET", "/api/v1/buckets", "token-alice", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"items"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/v1/buckets", "token-alice", `{"name":"Home"}`); w.Code != 201 || !strings.Contains(w.Body.String(), `"Home"`) {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/v1/buckets", "token-alice", `not json`); w.Code != 400 {
		t.Fatalf("bad json: %d", w.Code)
	}
	// A malformed id is "not found", like any bucket the person is not in.
	if w := do(h, "GET", "/api/v1/buckets/not-a-uuid", "token-alice", ""); w.Code != 404 {
		t.Fatalf("bad id: %d", w.Code)
	}
	// The confirmation name travels in the body of DELETE.
	if w := do(h, "DELETE", "/api/v1/buckets/"+id, "token-alice", `{"confirmName":"Home"}`); w.Code != 204 || fb.confirm != "Home" {
		t.Fatalf("delete: %d confirm %q", w.Code, fb.confirm)
	}
	if w := do(h, "PUT", "/api/v1/buckets/"+id+"/notify", "token-alice", `{}`); w.Code != 400 {
		t.Fatalf("notify without a value: %d", w.Code)
	}
	if w := do(h, "PUT", "/api/v1/buckets/"+id+"/notify", "token-alice", `{"notify":false}`); w.Code != 200 {
		t.Fatalf("notify: %d", w.Code)
	}
	if w := do(h, "POST", "/api/v1/buckets/"+id+"/invites", "token-alice", ""); w.Code != 201 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invite: %d cache %q", w.Code, w.Header().Get("Cache-Control"))
	}

	for _, tc := range []struct {
		err  error
		want int
	}{
		{buckets.ErrNotFound, 404}, {buckets.ErrNotOwner, 403}, {buckets.ErrValidation, 400},
		{buckets.ErrConflict, 409}, {buckets.ErrInviteGone, 410},
	} {
		fb.err = tc.err
		if w := do(h, "POST", "/api/v1/invites/join", "token-alice", `{"token":"x"}`); w.Code != tc.want {
			t.Errorf("%v: want %d, got %d", tc.err, tc.want, w.Code)
		}
	}
	// Specific conflicts keep their own code, so the app can explain them.
	for err, code := range map[error]string{buckets.ErrFull: "bucket_full", buckets.ErrAlreadyMember: "already_member",
		buckets.ErrTooManyBuckets: "bucket_limit", buckets.ErrTransferFirst: "transfer_first"} {
		fb.err = err
		w := do(h, "POST", "/api/v1/invites/join", "token-alice", `{"token":"x"}`)
		if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
			t.Errorf("%v: %d %s", err, w.Code, w.Body)
		}
	}
	fb.err = nil

	// A pasted code is passed through as typed; the service normalises it.
	if w := do(h, "POST", "/api/v1/invites/preview", "token-alice", `{"token":"abcd-efgh"}`); w.Code != 200 || fb.token != "abcd-efgh" {
		t.Fatalf("preview: %d token %q", w.Code, fb.token)
	}
}

func TestInviteLookupsAreRateLimited(t *testing.T) {
	alice := uuid.New()
	h := newRouterWith(func(d *Deps) {
		d.Users = &fakeUsers{ids: map[string]uuid.UUID{"alice": alice}}
		d.Buckets = &fakeBuckets{}
		d.Limits = ratelimit.Limits{InvitePerMinute: 3}
	})
	for i := 1; i <= 3; i++ {
		if w := do(h, "POST", "/api/v1/invites/join", "token-alice", `{"token":"x"}`); w.Code != 200 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	if w := do(h, "POST", "/api/v1/invites/preview", "token-alice", `{"token":"x"}`); w.Code != 429 {
		t.Fatalf("over the limit: %d", w.Code)
	}
	// Other routes are not charged to the invite budget.
	if w := do(h, "GET", "/api/v1/buckets", "token-alice", ""); w.Code != 200 {
		t.Fatalf("list after the limit: %d", w.Code)
	}
}
