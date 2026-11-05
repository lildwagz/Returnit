//go:build integration

package buckets_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/buckets"
	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/users"
)

type env struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	svc  *buckets.Service
	us   *users.Store
	made []uuid.UUID
}

// Run with: TEST_DATABASE_URL=... go test -tags integration ./internal/buckets
func newEnv(t *testing.T) *env {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, ctx: ctx, pool: pool, svc: buckets.NewService(pool, nil), us: users.NewStore(pool)}
	t.Cleanup(func() {
		for _, id := range e.made {
			// every bucket the test user is in (they are all test buckets), then their own
			pool.Exec(ctx, `DELETE FROM buckets WHERE id IN (SELECT bucket_id FROM bucket_members WHERE user_id = $1)`, id)
			pool.Exec(ctx, `DELETE FROM buckets WHERE created_by = $1`, id)
			pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		}
		pool.Close()
	})
	return e
}

func (e *env) user(name string) uuid.UUID {
	e.t.Helper()
	s := uuid.NewString()
	id, err := e.us.EnsureBySubject(e.ctx, "bk-"+name+"-"+s, "bk-"+name+"-"+s+"@example.com", name)
	if err != nil {
		e.t.Fatal(err)
	}
	e.made = append(e.made, id)
	return id
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func is(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func TestBucketLifecycle(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.user("Alice"), e.user("Bob"), e.user("Carol")

	// Everyone starts with their personal bucket, listed first.
	list, err := e.svc.List(e.ctx, alice)
	e.must(err)
	if len(list) != 1 || !list[0].Personal || list[0].Role != "owner" {
		t.Fatalf("new user's buckets: %+v", list)
	}
	personal := list[0]

	if _, err := e.svc.Create(e.ctx, alice, "  "); !errors.Is(err, buckets.ErrValidation) {
		t.Fatalf("blank name: %v", err)
	}
	home, err := e.svc.Create(e.ctx, alice, "Home")
	e.must(err)
	if home.Role != "owner" || home.MemberCount != 1 || home.Personal {
		t.Fatalf("created: %+v", home)
	}

	// Only the owner renames; a personal bucket cannot be renamed or shared.
	if _, err := e.svc.Rename(e.ctx, bob, home.ID, "X"); !errors.Is(err, buckets.ErrNotFound) {
		t.Fatalf("rename by a stranger: %v", err)
	}
	if _, err := e.svc.Rename(e.ctx, alice, personal.ID, "X"); !errors.Is(err, buckets.ErrValidation) {
		t.Fatalf("rename personal: %v", err)
	}
	if _, err := e.svc.CreateInvite(e.ctx, alice, personal.ID); !errors.Is(err, buckets.ErrValidation) {
		t.Fatalf("invite to personal: %v", err)
	}
	if _, err := e.svc.Rename(e.ctx, alice, home.ID, "Rumah"); err != nil {
		t.Fatal(err)
	}

	// Invite: stranger cannot create one; preview shows the bucket; join adds a member.
	if _, err := e.svc.CreateInvite(e.ctx, bob, home.ID); !errors.Is(err, buckets.ErrNotFound) {
		t.Fatalf("invite by a stranger: %v", err)
	}
	inv1, err := e.svc.CreateInvite(e.ctx, alice, home.ID)
	e.must(err)
	pv, err := e.svc.Preview(e.ctx, inv1.Token)
	e.must(err)
	if pv.BucketID != home.ID || pv.BucketName != "Rumah" || pv.MemberCount != 1 || pv.InvitedBy == nil || *pv.InvitedBy != "Alice" || pv.Full {
		t.Fatalf("preview: %+v", pv)
	}
	if _, err := e.svc.Preview(e.ctx, "NOTATOKEN"); !errors.Is(err, buckets.ErrInviteGone) {
		t.Fatalf("unknown token: %v", err)
	}
	// A new link revokes the old one.
	inv2, err := e.svc.CreateInvite(e.ctx, alice, home.ID)
	e.must(err)
	if _, err := e.svc.Join(e.ctx, bob, inv1.Token); !errors.Is(err, buckets.ErrInviteGone) {
		t.Fatalf("revoked invite: %v", err)
	}
	// The code can be typed with other case and hyphens.
	typed := inv2.Token[:4] + "-" + inv2.Token[4:]
	joined, err := e.svc.Join(e.ctx, bob, typed)
	e.must(err)
	if joined.Role != "member" || joined.MemberCount != 2 {
		t.Fatalf("joined: %+v", joined)
	}
	if _, err := e.svc.Join(e.ctx, bob, inv2.Token); !errors.Is(err, buckets.ErrConflict) {
		t.Fatalf("joining twice: %v", err)
	}

	// Expired invites stop working.
	_, err = e.pool.Exec(e.ctx, `UPDATE bucket_invites SET expires_at = now() - interval '1 minute' WHERE bucket_id = $1 AND revoked_at IS NULL`, home.ID)
	e.must(err)
	if _, err := e.svc.Join(e.ctx, carol, inv2.Token); !errors.Is(err, buckets.ErrInviteGone) {
		t.Fatalf("expired invite: %v", err)
	}

	// Members are shown by display name, never by e-mail.
	ms, err := e.svc.Members(e.ctx, bob, home.ID)
	e.must(err)
	if len(ms) != 2 || ms[0].Name != "Alice" || ms[0].Role != "owner" || !ms[1].You || ms[1].Name != "Bob" {
		t.Fatalf("members: %+v", ms)
	}
	if _, err := e.svc.Members(e.ctx, carol, home.ID); !errors.Is(err, buckets.ErrNotFound) {
		t.Fatalf("members for a stranger: %v", err)
	}

	// The list carries the first members for the avatars: the owner first, display names only.
	all, err := e.svc.List(e.ctx, bob)
	e.must(err)
	for _, b := range all {
		if b.ID != home.ID {
			continue
		}
		if len(b.Members) != 2 || b.Members[0].Name != "Alice" || b.Members[1].Name != "Bob" || b.MemberCount != 2 {
			t.Fatalf("member preview: %+v", b)
		}
	}

	// Mute is per person.
	b, err := e.svc.SetNotify(e.ctx, bob, home.ID, false)
	e.must(err)
	if b.Notify {
		t.Fatal("still notifying after mute")
	}
	if a, _ := e.svc.Get(e.ctx, alice, home.ID); !a.Notify {
		t.Fatal("Alice's setting changed too")
	}

	// Removing and leaving: only the owner removes others; the owner cannot leave; a member can.
	if err := e.svc.RemoveMember(e.ctx, bob, home.ID, alice); !errors.Is(err, buckets.ErrNotOwner) {
		t.Fatalf("member removing the owner: %v", err)
	}
	if err := e.svc.RemoveMember(e.ctx, alice, home.ID, alice); !errors.Is(err, buckets.ErrConflict) {
		t.Fatalf("owner leaving: %v", err)
	}
	if err := e.svc.RemoveMember(e.ctx, alice, personal.ID, alice); !errors.Is(err, buckets.ErrValidation) {
		t.Fatalf("leaving the personal bucket: %v", err)
	}
	e.must(e.svc.TransferOwner(e.ctx, alice, home.ID, bob))
	if err := e.svc.TransferOwner(e.ctx, alice, home.ID, bob); !errors.Is(err, buckets.ErrNotOwner) {
		t.Fatalf("transfer by the former owner: %v", err)
	}
	e.must(e.svc.RemoveMember(e.ctx, alice, home.ID, alice)) // now a plain member: may leave
	if _, err := e.svc.Get(e.ctx, alice, home.ID); !errors.Is(err, buckets.ErrNotFound) {
		t.Fatalf("after leaving: %v", err)
	}

	// Deleting needs the owner and the exact name, and takes the purchases and receipts with it.
	psvc := purchases.NewService(purchases.NewPGRepo(e.pool), nil)
	p, err := psvc.Create(e.ctx, bob, purchases.RawInput{ProductName: "Fridge", PurchaseDate: "2026-01-01", BucketID: &home.ID})
	e.must(err)
	if p.BucketID == nil || *p.BucketID != home.ID || p.AddedBy == nil || *p.AddedBy != "Bob" || !p.AddedByMe {
		t.Fatalf("purchase in a shared bucket: %+v", p)
	}
	if err := e.svc.Delete(e.ctx, bob, home.ID, "wrong"); !errors.Is(err, buckets.ErrValidation) {
		t.Fatalf("delete with a wrong name: %v", err)
	}
	e.must(e.svc.Delete(e.ctx, bob, home.ID, "Rumah"))
	if _, err := psvc.Get(e.ctx, bob, p.ID); !errors.Is(err, purchases.ErrNotFound) {
		t.Fatalf("purchase after its bucket was deleted: %v", err)
	}
}

func TestBucketLimits(t *testing.T) {
	e := newEnv(t)
	owner := e.user("Owner")
	b, err := e.svc.Create(e.ctx, owner, "Full")
	e.must(err)
	inv, err := e.svc.CreateInvite(e.ctx, owner, b.ID)
	e.must(err)
	for i := 1; i < buckets.MaxMembers; i++ {
		_, err := e.svc.Join(e.ctx, e.user("M"), inv.Token)
		e.must(err)
	}
	pv, err := e.svc.Preview(e.ctx, inv.Token)
	e.must(err)
	if !pv.Full || pv.MemberCount != buckets.MaxMembers {
		t.Fatalf("preview of a full bucket: %+v", pv)
	}
	if _, err := e.svc.Join(e.ctx, e.user("Late"), inv.Token); !errors.Is(err, buckets.ErrConflict) {
		t.Fatalf("seventh member: %v", err)
	}

	// At most five created shared buckets per person.
	for i := 1; i < buckets.MaxCreated; i++ {
		_, err := e.svc.Create(e.ctx, owner, "B")
		e.must(err)
	}
	if _, err := e.svc.Create(e.ctx, owner, "One too many"); !errors.Is(err, buckets.ErrConflict) {
		t.Fatalf("sixth bucket: %v", err)
	}
}

// Moving a purchase, and the filters, follow membership.
func TestPurchasesInBuckets(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("Alice"), e.user("Bob")
	home, err := e.svc.Create(e.ctx, alice, "Home")
	e.must(err)
	psvc := purchases.NewService(purchases.NewPGRepo(e.pool), nil)

	p, err := psvc.Create(e.ctx, alice, purchases.RawInput{ProductName: "TV", PurchaseDate: "2026-01-01"})
	e.must(err)
	if p.BucketID == nil || !p.AddedByMe {
		t.Fatalf("new purchase: %+v", p)
	}
	// Bob is not in Home: neither placing a purchase there nor moving one there is allowed.
	if _, err := psvc.Create(e.ctx, bob, purchases.RawInput{ProductName: "X", PurchaseDate: "2026-01-01", BucketID: &home.ID}); !errors.Is(err, purchases.ErrValidation) {
		t.Fatalf("create in someone else's bucket: %v", err)
	}
	moved, err := psvc.Update(e.ctx, alice, p.ID, purchases.RawInput{ProductName: "TV", PurchaseDate: "2026-01-01", BucketID: &home.ID})
	e.must(err)
	if *moved.BucketID != home.ID {
		t.Fatalf("moved: %+v", moved)
	}
	// An update without bucketId keeps the bucket.
	kept, err := psvc.Update(e.ctx, alice, p.ID, purchases.RawInput{ProductName: "TV 2", PurchaseDate: "2026-01-01"})
	e.must(err)
	if *kept.BucketID != home.ID {
		t.Fatalf("bucket lost on a plain update: %+v", kept)
	}
	if _, total, _ := psvc.List(e.ctx, alice, purchases.ListFilter{BucketID: &home.ID, Page: 1, PageSize: 10}); total != 1 {
		t.Fatalf("filter by bucket: %d", total)
	}
	// Filtering by a bucket the person is not in matches nothing (it does not reveal that the bucket exists).
	if _, total, _ := psvc.List(e.ctx, bob, purchases.ListFilter{BucketID: &home.ID, Page: 1, PageSize: 10}); total != 0 {
		t.Fatalf("filter by a foreign bucket: %d", total)
	}
	if s, _ := psvc.Summary(e.ctx, alice, &home.ID); s.Counts.Total != 1 {
		t.Fatalf("summary of one bucket: %+v", s)
	}
}

// Erasing an account hands shared buckets over, keeps shared purchases, and removes personal data.
func TestAccountPurgeAndBuckets(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("Alice"), e.user("Bob")
	home, err := e.svc.Create(e.ctx, alice, "Home")
	e.must(err)
	inv, err := e.svc.CreateInvite(e.ctx, alice, home.ID)
	e.must(err)
	_, err = e.svc.Join(e.ctx, bob, inv.Token)
	e.must(err)
	solo, err := e.svc.Create(e.ctx, alice, "Solo") // nobody else: goes away with Alice
	e.must(err)

	psvc := purchases.NewService(purchases.NewPGRepo(e.pool), nil)
	shared, err := psvc.Create(e.ctx, alice, purchases.RawInput{ProductName: "Shared fridge", PurchaseDate: "2026-01-01", BucketID: &home.ID})
	e.must(err)
	mine, err := psvc.Create(e.ctx, alice, purchases.RawInput{ProductName: "Alice's phone", PurchaseDate: "2026-01-01"})
	e.must(err)
	inSolo, err := psvc.Create(e.ctx, alice, purchases.RawInput{ProductName: "In solo", PurchaseDate: "2026-01-01", BucketID: &solo.ID})
	e.must(err)

	_, err = e.pool.Exec(e.ctx, `UPDATE users SET deletion_requested_at = now() - interval '8 days', deletion_scheduled_at = now() - interval '1 minute' WHERE id = $1`, alice)
	e.must(err)
	n, err := account.NewService(e.pool, 7*24*time.Hour).PurgeDue(e.ctx)
	e.must(err)
	if n < 1 {
		t.Fatalf("purged %d accounts", n)
	}

	// The shared bucket and its purchase survive; Bob owns it now and sees "added by" empty.
	got, err := psvc.Get(e.ctx, bob, shared.ID)
	e.must(err)
	if got.AddedBy != nil || got.AddedByMe {
		t.Fatalf("added by after the adder left: %+v", got)
	}
	b, err := e.svc.Get(e.ctx, bob, home.ID)
	e.must(err)
	if b.Role != "owner" {
		t.Fatalf("ownership was not handed over: %+v", b)
	}
	// Personal data and the bucket nobody else was in are gone.
	var left int
	e.must(e.pool.QueryRow(e.ctx, `SELECT count(*) FROM purchases WHERE id = ANY($1)`, []uuid.UUID{mine.ID, inSolo.ID}).Scan(&left))
	if left != 0 {
		t.Fatalf("%d of Alice's own purchases remain", left)
	}
	e.must(e.pool.QueryRow(e.ctx, `SELECT count(*) FROM buckets WHERE id = $1 OR (personal AND created_by = $2)`, solo.ID, alice).Scan(&left))
	if left != 0 {
		t.Fatalf("%d of Alice's buckets remain", left)
	}
}

// With the choice "erase shared too", the purchases the person added to a shared bucket go with the account.
func TestAccountPurgeErasesSharedWhenChosen(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("Alice"), e.user("Bob")
	home, err := e.svc.Create(e.ctx, alice, "Home")
	e.must(err)
	inv, err := e.svc.CreateInvite(e.ctx, alice, home.ID)
	e.must(err)
	_, err = e.svc.Join(e.ctx, bob, inv.Token)
	e.must(err)
	psvc := purchases.NewService(purchases.NewPGRepo(e.pool), nil)
	hers, err := psvc.Create(e.ctx, alice, purchases.RawInput{ProductName: "Alice's in Home", PurchaseDate: "2026-01-01", BucketID: &home.ID})
	e.must(err)
	his, err := psvc.Create(e.ctx, bob, purchases.RawInput{ProductName: "Bob's in Home", PurchaseDate: "2026-01-01", BucketID: &home.ID})
	e.must(err)

	acc := account.NewService(e.pool, 7*24*time.Hour)
	_, err = e.pool.Exec(e.ctx, `UPDATE users SET deletion_requested_at = now() - interval '8 days', deletion_scheduled_at = now() - interval '1 minute' WHERE id = $1`, alice)
	e.must(err)
	e.must(acc.SetEraseShared(e.ctx, alice, true))
	if _, err := acc.PurgeDue(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := psvc.Get(e.ctx, bob, hers.ID); !errors.Is(err, purchases.ErrNotFound) {
		t.Fatalf("Alice's shared purchase should be erased: %v", err)
	}
	if _, err := psvc.Get(e.ctx, bob, his.ID); err != nil {
		t.Fatalf("Bob's purchase must stay: %v", err)
	}
	if b, err := e.svc.Get(e.ctx, bob, home.ID); err != nil || b.Role != "owner" {
		t.Fatalf("the bucket and Bob's ownership must stay: %+v %v", b, err)
	}
}
