package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleOidcDiscovery_returnsExpectedShape(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/oauth/.well-known/openid-configuration", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var res oidcDiscoveryResponse
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	wantBase := testBaseURL
	checks := map[string]string{
		"issuer":                 res.Issuer,
		"authorization_endpoint": res.AuthorizationEndpoint,
		"token_endpoint":         res.TokenEndpoint,
		"userinfo_endpoint":      res.UserinfoEndpoint,
	}
	for k, v := range checks {
		if v == "" {
			t.Errorf("%s is empty", k)
		}
		if !strings.HasPrefix(v, wantBase) {
			t.Errorf("%s = %q; want prefix %q", k, v, wantBase)
		}
	}
	if len(res.ResponseTypesSupported) == 0 {
		t.Error("response_types_supported is empty")
	}
}

func TestHandleOidcDiscovery_bothPaths(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{
		"/kobo/" + testToken + "/oauth/.well-known/openid-configuration",
		"/kobo/" + testToken + "/.well-known/openid-configuration",
	} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", testExternalURL+path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("path %q: want 200, got %d", path, w.Code)
		}
		var res oidcDiscoveryResponse
		if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
			t.Errorf("path %q: decode failed: %v", path, err)
		}
		if res.Issuer == "" {
			t.Errorf("path %q: issuer is empty", path)
		}
	}
}

func TestHandleOAuth_returnsTokens(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("POST", testBaseURL+"/oauth/token", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var res oauthTokenResponse
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if res.AccessToken == "" {
		t.Error("access_token is empty")
	}
	if res.RefreshToken == "" {
		t.Error("refresh_token is empty")
	}
	if res.ExpiresIn != 3600 {
		t.Errorf("expires_in: want 3600, got %d", res.ExpiresIn)
	}
	if res.TokenType != "Bearer" {
		t.Errorf("token_type: want Bearer, got %q", res.TokenType)
	}
	if res.AccessTokenAlt != res.AccessToken {
		t.Error("AccessToken (PascalCase) does not match access_token")
	}
}

func TestHandleOAuth_subpathFallback(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{
		"/kobo/" + testToken + "/oauth/authorize",
		"/kobo/" + testToken + "/oauth/refresh",
		"/kobo/" + testToken + "/oauth/userinfo",
		"/kobo/" + testToken + "/oauth/somethingelse",
	} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", testExternalURL+path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("path %q: want 200, got %d", path, w.Code)
		}
		var got any
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Errorf("path %q: not valid JSON: %v", path, err)
		}
	}
}

func TestHandleLibraryState_echoesReadingState(t *testing.T) {
	srv := newTestServer(t)
	body := `{"ReadingStates":[{"EntitlementId":"aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb","StatusInfo":{"Status":"Reading","LastModified":"2026-05-14T10:00:00Z"},"Statistics":{"SpentReadingMinutes":5,"RemainingTimeMinutes":120,"LastModified":"2026-05-14T10:00:00Z"}}]}`
	req := httptest.NewRequest("PUT", testBaseURL+"/v1/library/aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb/state", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var res map[string]any
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if res["EntitlementId"] != "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb" {
		t.Errorf("EntitlementId: want %q, got %v", "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb", res["EntitlementId"])
	}
	if res["ReadingStateModified"] == "" || res["ReadingStateModified"] == nil {
		t.Error("ReadingStateModified is missing or empty")
	}
	si, _ := res["StatusInfo"].(map[string]any)
	if si == nil || si["Status"] != "Reading" {
		t.Errorf("StatusInfo.Status: want Reading, got %v", si)
	}
}

func TestHandleLibraryState_emptyBody(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("PUT", testBaseURL+"/v1/library/aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb/state", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	// Empty body should return an empty JSON array, not an error.
	body := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(body, "[") {
		t.Errorf("want JSON array for empty body, got: %s", body)
	}
}

func TestHandleLibraryState_malformedJSON(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("PUT", testBaseURL+"/v1/library/aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb/state", strings.NewReader("{bad json"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var got any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestHandleAuthDevice_returnsTokens(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("POST", testBaseURL+"/v1/auth/device", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var res authDeviceResponse
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if res.AccessToken == "" {
		t.Error("AccessToken is empty")
	}
	if res.RefreshToken == "" {
		t.Error("RefreshToken is empty")
	}
	if res.TokenType != "Bearer" {
		t.Errorf("TokenType: want %q, got %q", "Bearer", res.TokenType)
	}
	if res.UserKey == "" {
		t.Error("UserKey is empty")
	}
}

func TestHandleAuthRefresh_sameShape(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("POST", testBaseURL+"/v1/auth/refresh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var res authDeviceResponse
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if res.AccessToken == "" || res.TokenType != "Bearer" {
		t.Errorf("unexpected response: %+v", res)
	}
}

func TestHandleUserProfile_hasRequiredFields(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/v1/user/profile", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var res userProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if res.UserId == "" {
		t.Error("UserId is empty")
	}
	if res.UserKey == "" {
		t.Error("UserKey is empty")
	}
	if res.UserDisplayName == "" {
		t.Error("UserDisplayName is empty")
	}
}

func TestHandleLibrarySync_emptyResponse(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/v1/library/sync", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if w.Header().Get("x-kobo-sync") != "done" {
		t.Errorf("x-kobo-sync: want %q, got %q", "done", w.Header().Get("x-kobo-sync"))
	}
	if w.Header().Get("x-kobo-synctoken") == "" {
		t.Error("x-kobo-synctoken header is missing")
	}
	// Must be a JSON array, not an object — device rejects {} for this endpoint.
	body := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(body, "[") {
		t.Errorf("body must be a JSON array, got: %s", body)
	}
}

func TestHandleLibrarySync_echosSyncToken(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/v1/library/sync", nil)
	req.Header.Set("x-kobo-synctoken", "previous-token-abc")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if got := w.Header().Get("x-kobo-synctoken"); got != "previous-token-abc" {
		t.Errorf("want echoed token %q, got %q", "previous-token-abc", got)
	}
}

func TestHandleInitialization_status(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/v1/initialization", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("want application/json, got %q", ct)
	}
	if got := w.Header().Get("x-kobo-apitoken"); got == "" {
		t.Error("x-kobo-apitoken header is missing")
	}
}

func TestHandleInitialization_syncURLsPointAtServer(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/v1/initialization", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var outer initResponse
	if err := json.NewDecoder(w.Body).Decode(&outer); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	res := outer.Resources

	wantBase := testBaseURL
	syncCritical := map[string]string{
		"library_sync":          res.LibrarySync,
		"device_auth":           res.DeviceAuth,
		"device_refresh":        res.DeviceRefresh,
		"reading_state":         res.ReadingState,
		"tags":                  res.Tags,
		"post_analytics_event":  res.PostAnalyticsEvent,
		"user_loyalty_benefits": res.UserLoyaltyBenefits,
		"affiliaterequest":      res.AffiliateRequest,
		"reading_services_host": res.ReadingServicesHost,
	}
	for key, got := range syncCritical {
		if !strings.HasPrefix(got, wantBase) {
			t.Errorf("resources[%q] = %q; want prefix %q", key, got, wantBase)
		}
	}
}

func TestHandleInitialization_noStoreAPIURLs(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/v1/initialization", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	// No URL in the response should point back at the real Kobo cloud.
	if strings.Contains(w.Body.String(), "storeapi.kobo.com") {
		t.Error("response contains storeapi.kobo.com; all URLs should point at our server")
	}
}

func TestHandleInitialization_useOneStore(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/v1/initialization", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var outer initResponse
	json.NewDecoder(w.Body).Decode(&outer)
	if outer.Resources.UseOneStore != "False" {
		t.Errorf("use_one_store: want %q, got %q", "False", outer.Resources.UseOneStore)
	}
}

func TestHandleInitialization_allFieldsNonEmpty(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/v1/initialization", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	// Decode via the wrapper then check all resource fields for emptiness.
	var outer struct {
		Resources map[string]string `json:"Resources"`
	}
	if err := json.NewDecoder(w.Body).Decode(&outer); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(outer.Resources) == 0 {
		t.Fatal("Resources map is empty or missing")
	}
	for k, v := range outer.Resources {
		if v == "" {
			t.Errorf("resources[%q] is empty", k)
		}
	}
}

func TestHandleInitialization_tokenIsolation(t *testing.T) {
	// Initialization under a different token should return 401, not mix up resources.
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", testExternalURL+"/kobo/badtoken/v1/initialization", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}
