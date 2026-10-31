package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/auth"
	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/reminders"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

// fakeVerifier accepts tokens of the form "token-<subject>".
type fakeVerifier struct{}

func (fakeVerifier) Verify(_ context.Context, raw string) (auth.Claims, error) {
	if len(raw) > 6 && raw[:6] == "token-" {
		return auth.Claims{Subject: raw[6:], Email: raw[6:] + "@example.com", AuthTime: time.Now()}, nil
	}
	// "old-<sub>": logged in an hour ago. "noauth-<sub>": provider sent no auth_time.
	if len(raw) > 4 && raw[:4] == "old-" {
		return auth.Claims{Subject: raw[4:], AuthTime: time.Now().Add(-time.Hour)}, nil
	}
	if len(raw) > 7 && raw[:7] == "noauth-" {
		return auth.Claims{Subject: raw[7:]}, nil
	}
	return auth.Claims{}, errors.New("bad token")
}

type fakeUsers struct{ ids map[string]uuid.UUID }

func (f *fakeUsers) EnsureBySubject(_ context.Context, sub, _, _ string) (uuid.UUID, error) {
	if id, ok := f.ids[sub]; ok {
		return id, nil
	}
	id := uuid.New()
	f.ids[sub] = id
	return id, nil
}

// memRepo is an in-memory repository enforcing the same ownership rules as PGRepo.
type memRepo struct{ rows map[uuid.UUID]memRow }
type memRow struct {
	owner uuid.UUID
	p     purchases.Purchase
}

func (m *memRepo) Create(_ context.Context, u uuid.UUID, in purchases.Input) (purchases.Purchase, error) {
	p := purchases.Purchase{ID: uuid.New(), ProductName: in.ProductName, Currency: in.Currency,
		PurchaseDate: purchases.Date{Time: in.PurchaseDate}}
	m.rows[p.ID] = memRow{u, p}
	return p, nil
}
func (m *memRepo) Get(_ context.Context, u, id uuid.UUID) (purchases.Purchase, error) {
	r, ok := m.rows[id]
	if !ok || r.owner != u {
		return purchases.Purchase{}, purchases.ErrNotFound
	}
	return r.p, nil
}
func (m *memRepo) List(_ context.Context, u uuid.UUID, f purchases.ListFilter) ([]purchases.Purchase, int, error) {
	out := []purchases.Purchase{}
	for _, r := range m.rows {
		if r.owner == u {
			out = append(out, r.p)
		}
	}
	return out, len(out), nil
}
func (m *memRepo) Update(ctx context.Context, u, id uuid.UUID, in purchases.Input) (purchases.Purchase, error) {
	r, err := m.Get(ctx, u, id)
	if err != nil {
		return r, err
	}
	r.ProductName = in.ProductName
	m.rows[id] = memRow{u, r}
	return r, nil
}
func (m *memRepo) Delete(ctx context.Context, u, id uuid.UUID) (*string, error) {
	if _, err := m.Get(ctx, u, id); err != nil {
		return nil, err
	}
	delete(m.rows, id)
	return nil, nil
}

func newTestRouter(db Pinger) http.Handler { return newRouterWith(func(d *Deps) { d.DB = db }) }

func newRouterWith(mod func(*Deps)) http.Handler {
	d := Deps{
		DB:             fakeDB{},
		Verifier:       fakeVerifier{},
		Users:          &fakeUsers{ids: map[string]uuid.UUID{}},
		Purchases:      purchases.NewService(&memRepo{rows: map[uuid.UUID]memRow{}}, nil),
		Reminders:      reminders.NewService(&memPrefs{rows: map[uuid.UUID]reminders.Preferences{}}),
		Notifications:  &memNotifs{rows: map[uuid.UUID]*memNotif{}},
		RequestTimeout: time.Second,
	}
	mod(&d)
	return NewRouter(d)
}

func do(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHealth(t *testing.T) {
	h := newTestRouter(fakeDB{})
	if w := do(h, "GET", "/health/live", "", ""); w.Code != 200 || w.Header().Get(requestIDHeader) == "" {
		t.Fatalf("live: %d", w.Code)
	}
	if w := do(h, "GET", "/health/ready", "", ""); w.Code != 200 {
		t.Fatalf("ready: %d", w.Code)
	}
	if w := do(newTestRouter(fakeDB{errors.New("down")}), "GET", "/health/ready", "", ""); w.Code != 503 {
		t.Fatalf("not ready: %d", w.Code)
	}
}

func TestNotFoundEnvelope(t *testing.T) {
	if w := do(newTestRouter(fakeDB{}), "GET", "/nope", "", ""); w.Code != 404 {
		t.Fatalf("code=%d", w.Code)
	}
}

func TestAuthRequired(t *testing.T) {
	h := newTestRouter(fakeDB{})
	for _, tok := range []string{"", "garbage"} {
		if w := do(h, "GET", "/api/v1/purchases", tok, ""); w.Code != 401 {
			t.Fatalf("token %q: code=%d", tok, w.Code)
		}
	}
}

const validBody = `{"productName":"  Laptop ","purchaseDate":"2026-01-31"}`

func TestPurchaseCRUDAndOwnerIsolation(t *testing.T) {
	h := newTestRouter(fakeDB{})

	w := do(h, "POST", "/api/v1/purchases", "token-alice", validBody)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var p struct {
		ID, ProductName, Currency, PurchaseDate string
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.ProductName != "Laptop" || p.Currency != "IDR" || p.PurchaseDate != "2026-01-31" {
		t.Fatalf("unexpected body: %+v", p)
	}
	path := "/api/v1/purchases/" + p.ID

	if w := do(h, "GET", path, "token-alice", ""); w.Code != 200 {
		t.Fatalf("owner get: %d", w.Code)
	}
	// Bob must get 404 (not 403) on every operation against Alice's record.
	for _, tc := range [][2]string{{"GET", ""}, {"PATCH", validBody}, {"DELETE", ""}} {
		if w := do(h, tc[0], path, "token-bob", tc[1]); w.Code != 404 {
			t.Fatalf("bob %s: code=%d", tc[0], w.Code)
		}
	}
	if w := do(h, "GET", "/api/v1/purchases", "token-bob", ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"total":0`)) {
		t.Fatalf("bob list leaked: %s", w.Body)
	}
	if w := do(h, "DELETE", path, "token-alice", ""); w.Code != 204 {
		t.Fatalf("owner delete: %d", w.Code)
	}
	if w := do(h, "GET", "/api/v1/purchases/not-a-uuid", "token-alice", ""); w.Code != 404 {
		t.Fatalf("bad id: %d", w.Code)
	}
}

func TestPurchaseValidationErrors(t *testing.T) {
	h := newTestRouter(fakeDB{})
	for name, body := range map[string]string{
		"missing name":  `{"purchaseDate":"2026-01-01"}`,
		"bad date":      `{"productName":"x","purchaseDate":"01-01-2026"}`,
		"negative":      `{"productName":"x","purchaseDate":"2026-01-01","price":-1}`,
		"bad currency":  `{"productName":"x","purchaseDate":"2026-01-01","currency":"RUPIAH"}`,
		"end<start":     `{"productName":"x","purchaseDate":"2026-01-01","warrantyStartDate":"2026-02-01","warrantyEndDate":"2026-01-01"}`,
		"malformed":     `{`,
		"client userid": `{"productName":"x","purchaseDate":"2026-01-01","user_id":"x"}`, // ignored, still valid
	} {
		w := do(h, "POST", "/api/v1/purchases", "token-alice", body)
		want := 400
		if name == "client userid" {
			want = 201
		}
		if w.Code != want {
			t.Errorf("%s: code=%d body=%s", name, w.Code, w.Body)
		}
	}
}

// Deadlines of the in-memory fake: none (it has no dates; the SQL is covered by an integration test).
func (m *memRepo) Deadlines(context.Context, uuid.UUID, int) ([]purchases.Deadline, error) {
	return []purchases.Deadline{}, nil
}

// Summary counts the owner's rows only (the in-memory fake has no warranty dates; the SQL is covered by an integration test).
func (m *memRepo) Summary(_ context.Context, u uuid.UUID) (purchases.Summary, error) {
	s := purchases.Summary{CoveredValue: []purchases.CurrencyAmount{}}
	for _, r := range m.rows {
		if r.owner == u {
			s.Counts.Total++
			s.Counts.Unknown++
		}
	}
	return s, nil
}
