package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gophish/gophish/models"
	"github.com/pquerna/otp/totp"
)

func TestTwoFactorUserPolicyAPI(t *testing.T) {
	tc := setupTest(t)
	request := func(method, path, key string, body interface{}, status int) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		tc.apiServer.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body)
		}
		return w
	}
	required := true
	payload := userRequest{Username: "required", Password: "test-password", Role: models.RoleUser, TwoFactorRequired: &required}
	request("POST", "/api/users/", tc.admin.ApiKey, payload, http.StatusOK)
	u, err := models.GetUserByUsername(payload.Username)
	if err != nil {
		t.Fatal(err)
	}
	if !u.TwoFactorRequired {
		t.Fatal("creation lost the 2FA requirement")
	}
	path := fmt.Sprintf("/api/users/%d", u.Id)
	// Legacy clients omitting the field must preserve the requirement.
	payload.TwoFactorRequired = nil
	payload.Password = ""
	request("PUT", path, tc.admin.ApiKey, payload, http.StatusOK)
	u, _ = models.GetUser(u.Id)
	if !u.TwoFactorRequired {
		t.Fatal("omitted field disabled 2FA")
	}
	request("GET", path, u.ApiKey, nil, http.StatusForbidden)
	token, challenge, err := models.BeginTwoFactor(u, models.TwoFactorSetup)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(challenge.Secret, time.Now())
	if _, err = models.CompleteTwoFactor(token, code, "Phone"); err != nil {
		t.Fatal(err)
	}
	required = false
	payload.TwoFactorRequired = &required
	request("PUT", path, u.ApiKey, payload, http.StatusForbidden)
	u, _ = models.GetUser(u.Id)
	if !u.TwoFactorRequired {
		t.Fatal("regular user disabled requirement")
	}
	request("PUT", path, tc.admin.ApiKey, payload, http.StatusOK)
	u, _ = models.GetUser(u.Id)
	if u.TwoFactorRequired || len(u.TwoFactorMethods) != 1 {
		t.Fatal("policy toggle should preserve methods")
	}
}
