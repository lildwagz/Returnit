package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/returnit/returnit/internal/appconfig"
)

type fakeAppConfig struct{ cfg appconfig.Config }

func (f fakeAppConfig) Current() appconfig.Config { return f.cfg }

func routerWithAppConfig(cfg appconfig.Config) http.Handler {
	return newRouterWith(func(d *Deps) { d.AppConfig = fakeAppConfig{cfg} })
}

func getWith(h http.Handler, path, token string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

var forced = appconfig.Config{
	Android: appconfig.Platform{
		MinVersion: "1.2.0", LatestVersion: "1.4.0",
		DownloadURL: "https://dl.example.test/returnit-latest.apk", SHA256: "abc", SizeBytes: 42,
	},
	Message: "Perbarui aplikasi.",
}

func TestAppConfigEndpointIsPublic(t *testing.T) {
	h := routerWithAppConfig(forced)
	w := getWith(h, "/api/v1/app-config?platform=android&version=1.1.0", "", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "update_required" || got["minVersion"] != "1.2.0" || got["downloadUrl"] != "https://dl.example.test/returnit-latest.apk" || got["message"] != "Perbarui aplikasi." {
		t.Fatalf("unexpected body: %v", got)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("the answer must not be cached")
	}

	for version, want := range map[string]string{"1.3.0": "update_available", "1.4.0": "ok", "": "ok"} {
		w := getWith(h, "/api/v1/app-config?platform=android&version="+version, "", nil)
		json.Unmarshal(w.Body.Bytes(), &got)
		if got["status"] != want {
			t.Errorf("version %q: status %v want %s", version, got["status"], want)
		}
	}
	// the version may also come from the header
	w = getWith(h, "/api/v1/app-config", "", map[string]string{appVersionHeader: "1.0.0"})
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["status"] != "update_required" {
		t.Errorf("header version: %v", got["status"])
	}
}

func TestAppConfigDisabledMeansOK(t *testing.T) {
	w := getWith(newTestRouter(fakeDB{}), "/api/v1/app-config?version=0.0.1", "", nil)
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got["status"] != "ok" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
}

func TestVersionGateRefusesOldApps(t *testing.T) {
	h := routerWithAppConfig(forced)

	w := getWith(h, "/api/v1/purchases", "token-a", map[string]string{appVersionHeader: "1.1.9"})
	if w.Code != http.StatusUpgradeRequired {
		t.Fatalf("old version: code=%d", w.Code)
	}
	var body struct {
		Error struct {
			Code        string `json:"code"`
			MinVersion  string `json:"minVersion"`
			DownloadURL string `json:"downloadUrl"`
		} `json:"error"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Code != "app_update_required" || body.Error.MinVersion != "1.2.0" || body.Error.DownloadURL == "" {
		t.Fatalf("body: %s", w.Body)
	}
	if w.Header().Get("X-Min-App-Version") != "1.2.0" {
		t.Fatal("missing X-Min-App-Version")
	}

	// the gate runs before authentication, so an old app learns it must update even when its token is stale
	if w := getWith(h, "/api/v1/purchases", "", map[string]string{appVersionHeader: "1.0.0"}); w.Code != http.StatusUpgradeRequired {
		t.Fatalf("old version without token: code=%d", w.Code)
	}

	// current, newer, and unknown versions are not blocked
	for _, hdr := range []map[string]string{{appVersionHeader: "1.2.0"}, {appVersionHeader: "2.0.0"}, nil, {appVersionHeader: "not-a-version"}} {
		if w := getWith(h, "/api/v1/purchases", "token-a", hdr); w.Code != 200 {
			t.Fatalf("headers %v: code=%d", hdr, w.Code)
		}
	}

	// the health checks are never gated
	if w := getWith(h, "/health/live", "", map[string]string{appVersionHeader: "0.0.1"}); w.Code != 200 {
		t.Fatalf("health: %d", w.Code)
	}
}

func TestVersionGateIsPerPlatform(t *testing.T) {
	h := routerWithAppConfig(forced)
	// nothing is configured for iOS, so an old iOS build is not blocked by the Android minimum
	w := getWith(h, "/api/v1/purchases", "token-a", map[string]string{appVersionHeader: "1.0.0", appPlatformHeader: "ios"})
	if w.Code != 200 {
		t.Fatalf("ios: code=%d", w.Code)
	}
}

func TestMaintenanceStopsEveryone(t *testing.T) {
	h := routerWithAppConfig(appconfig.Config{Maintenance: true, MaintenanceMessage: "Sebentar lagi kembali."})
	for _, hdr := range []map[string]string{nil, {appVersionHeader: "9.9.9"}} {
		w := getWith(h, "/api/v1/purchases", "token-a", hdr)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Fatalf("headers %v: code=%d retry=%q", hdr, w.Code, w.Header().Get("Retry-After"))
		}
	}
	// the app can still ask what is going on, and the health checks stay up
	w := getWith(h, "/api/v1/app-config?version=1.0.0", "", nil)
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got["status"] != "maintenance" || got["message"] != "Sebentar lagi kembali." {
		t.Fatalf("app-config during maintenance: %d %s", w.Code, w.Body)
	}
	if w := getWith(h, "/health/ready", "", nil); w.Code != 200 {
		t.Fatalf("health: %d", w.Code)
	}
}
