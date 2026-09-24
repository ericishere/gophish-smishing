package controllers

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gophish/gophish/auth"
	"github.com/gophish/gophish/middleware/ratelimit"
	"github.com/gophish/gophish/models"
	"github.com/pquerna/otp/totp"
)

type twoFactorBrowser struct {
	t      *testing.T
	client *http.Client
	base   string
	csrf   string
}

func newTwoFactorBrowser(t *testing.T, base string) *twoFactorBrowser {
	jar, _ := cookiejar.New(nil)
	b := &twoFactorBrowser{t: t, base: base, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	b.get("/login", http.StatusOK)
	return b
}

func (b *twoFactorBrowser) request(method, path string, values url.Values, status int) (string, *http.Response) {
	b.t.Helper()
	var body io.Reader
	if values != nil {
		values.Set("csrf_token", b.csrf)
		body = strings.NewReader(values.Encode())
	}
	req, _ := http.NewRequest(method, b.base+path, body)
	if values != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", b.base)
		req.Header.Set("Referer", b.base+path)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		b.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, status, data)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		doc, _ := goquery.NewDocumentFromReader(strings.NewReader(string(data)))
		if token, exists := doc.Find("input[name=csrf_token]").First().Attr("value"); exists {
			b.csrf = token
		}
	}
	return string(data), resp
}
func (b *twoFactorBrowser) get(path string, status int) string {
	body, _ := b.request("GET", path, nil, status)
	return body
}
func (b *twoFactorBrowser) post(path string, values url.Values, status int) *http.Response {
	_, resp := b.request("POST", path, values, status)
	return resp
}
func (b *twoFactorBrowser) login(name, password string) *http.Response {
	return b.post("/login", url.Values{"username": {name}, "password": {password}}, http.StatusFound)
}

func setupTwoFactorRoutes(t *testing.T) (*testContext, *httptest.Server) {
	tc := setupTest(t)
	// Keep the DB-backed per-account OTP limiter enabled; raise the generic IP
	// budget so a complete browser workflow doesn't need wall-clock sleeps.
	server := httptest.NewServer(NewAdminServer(tc.config.AdminConf, func(as *AdminServer) {
		as.limiter = ratelimit.NewPostLimiter(ratelimit.WithRequestsPerMinute(1000))
	}).server.Handler)
	t.Cleanup(func() { server.Close(); tearDown(t, tc) })
	return tc, server
}

func createTwoFactorUser(t *testing.T, name string, required, resetPassword bool) models.User {
	t.Helper()
	role, err := models.GetRoleBySlug(models.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.GeneratePasswordHash("test-password")
	u := models.User{Username: name, Hash: hash, ApiKey: auth.GenerateSecureKey(32), Role: role, RoleID: role.ID, TwoFactorRequired: required, PasswordChangeRequired: resetPassword}
	if err = models.PutUser(&u); err != nil {
		t.Fatal(err)
	}
	return u
}

func setupSecret(t *testing.T, html string) string {
	t.Helper()
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	secret, _ := doc.Find("#setup-key").Attr("value")
	if secret == "" {
		t.Fatal("enrollment secret missing")
	}
	return secret
}

func TestRequiredTwoFactorBrowserFlow(t *testing.T) {
	_, server := setupTwoFactorRoutes(t)
	u := createTwoFactorUser(t, "required-user", true, true)
	b := newTwoFactorBrowser(t, server.URL)
	if got := b.login(u.Username, "test-password").Header.Get("Location"); got != "/two_factor" {
		t.Fatal(got)
	}
	body := b.get("/two_factor", http.StatusOK)
	if strings.Contains(body, u.ApiKey) {
		t.Fatal("API key leaked before enrollment")
	}
	b.get("/settings", http.StatusTemporaryRedirect)
	b.get("/reset_password", http.StatusTemporaryRedirect)
	b.get("/api/users/1", http.StatusUnauthorized)
	b.get("/api/groups/?api_key="+u.ApiKey, http.StatusForbidden)
	pending, _ := models.GetUser(u.Id)
	if !pending.LastLogin.IsZero() {
		t.Fatal("updated last login before 2FA")
	}
	b.get("/two_factor/qr", http.StatusOK)
	secret := setupSecret(t, body)
	b.post("/two_factor", url.Values{"code": {"bad"}, "name": {"Phone"}}, http.StatusBadRequest)
	code, _ := totp.GenerateCode(secret, time.Now())
	b.post("/two_factor", url.Values{"code": {code}, "name": {"Phone"}}, http.StatusFound)
	// Required password change remains enforced after enrollment.
	b.get("/", http.StatusTemporaryRedirect)
	b.get("/reset_password", http.StatusOK)
	b.post("/reset_password", url.Values{"password": {"replacement-password"}, "confirm_password": {"replacement-password"}}, http.StatusFound)
	body = b.get("/settings", http.StatusOK)
	if !strings.Contains(body, "Keep at least one method") {
		t.Fatal("last-method deletion not disabled in settings")
	}
	fresh, _ := models.GetUser(u.Id)
	if len(fresh.TwoFactorMethods) != 1 || fresh.LastLogin.IsZero() {
		t.Fatal("enrollment not completed")
	}
	b.post("/settings/two_factor", url.Values{"action": {"delete"}, "current_password": {"replacement-password"}, "method_id": {fmt.Sprint(fresh.TwoFactorMethods[0].ID)}}, http.StatusSeeOther)
	fresh, _ = models.GetUser(u.Id)
	if len(fresh.TwoFactorMethods) != 1 {
		t.Fatal("last-method deletion succeeded")
	}
	b.get("/logout", http.StatusFound)
	b.get("/two_factor/qr", http.StatusNotFound)
	b.get("/login", http.StatusOK)
	b.login(u.Username, "replacement-password")
	body = b.get("/two_factor", http.StatusOK)
	if strings.Contains(body, "setup-key") || strings.Contains(body, secret) {
		t.Fatal("login redisclosed enrolled secret")
	}
	// Enrollment consumes its code; a later unused time step can sign in.
	code, _ = totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	b.post("/two_factor", url.Values{"code": {code}}, http.StatusFound)
	b.get("/settings", http.StatusOK)
	if err := models.ResetTwoFactor(u.Id); err != nil {
		t.Fatal(err)
	}
	b.get("/settings", http.StatusTemporaryRedirect)
	b.get("/login", http.StatusOK)
	b.login(u.Username, "replacement-password")
	body = b.get("/two_factor", http.StatusOK)
	if setupSecret(t, body) == secret {
		t.Fatal("reset reused secret")
	}
}

func TestOptionalTwoFactorSettingsAndReset(t *testing.T) {
	_, server := setupTwoFactorRoutes(t)
	u := createTwoFactorUser(t, "optional-user", false, false)
	b := newTwoFactorBrowser(t, server.URL)
	b.login(u.Username, "test-password")
	b.get("/settings", http.StatusOK)
	// Enrollment operations reject missing CSRF before evaluating credentials.
	req, _ := http.NewRequest("POST", server.URL+"/settings/two_factor", strings.NewReader("action=add&current_password=test-password"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("missing CSRF allowed", resp.StatusCode)
	}
	b.post("/settings/two_factor", url.Values{"action": {"add"}, "current_password": {"wrong-password"}}, http.StatusSeeOther)
	if got := b.get("/settings", http.StatusOK); !strings.Contains(got, "Invalid Password") {
		t.Fatal("password reauthentication missing")
	}
	b.post("/settings/two_factor", url.Values{"action": {"add"}, "current_password": {"test-password"}}, http.StatusSeeOther)
	body := b.get("/settings/two_factor", http.StatusOK)
	secret := setupSecret(t, body)
	code, _ := totp.GenerateCode(secret, time.Now())
	b.post("/settings/two_factor", url.Values{"action": {"confirm"}, "name": {"Personal phone"}, "code": {code}}, http.StatusSeeOther)
	b.get("/settings", http.StatusOK)
	fresh, _ := models.GetUser(u.Id)
	if len(fresh.TwoFactorMethods) != 1 {
		t.Fatal("optional method not added")
	}
	// A regular user cannot reset another user's methods.
	b.post("/users/1/two_factor/reset", url.Values{"current_password": {"test-password"}}, http.StatusForbidden)
	admin, _ := models.GetUser(1)
	admin.PasswordChangeRequired = false
	if err = models.PutUser(&admin); err != nil {
		t.Fatal(err)
	}
	a := newTwoFactorBrowser(t, server.URL)
	a.login("admin", "gophish")
	a.get("/users", http.StatusOK)
	// Impersonation must enter the same TOTP challenge.
	if loc := a.post("/impersonate", url.Values{"username": {u.Username}}, http.StatusFound).Header.Get("Location"); loc != "/two_factor" {
		t.Fatal("impersonation bypassed 2FA", loc)
	}
	a.get("/settings", http.StatusTemporaryRedirect)
	a.get("/logout", http.StatusFound)
	a.get("/login", http.StatusOK)
	a.login("admin", "gophish")
	path := fmt.Sprintf("/users/%d/two_factor/reset", u.Id)
	a.post(path, url.Values{"current_password": {"wrong"}}, http.StatusForbidden)
	a.post(path, url.Values{"current_password": {"gophish"}}, http.StatusOK)
	b.get("/settings", http.StatusTemporaryRedirect)
	fresh, _ = models.GetUser(u.Id)
	if len(fresh.TwoFactorMethods) != 0 {
		t.Fatal("admin reset failed")
	}
}
