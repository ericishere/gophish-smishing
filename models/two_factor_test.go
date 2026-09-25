package models

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"bitbucket.org/liamstask/goose/lib/goose"
	"github.com/gophish/gophish/auth"
	"github.com/gophish/gophish/config"
	"github.com/pquerna/otp/totp"
)

func setupTwoFactorTest(t *testing.T) User {
	t.Helper()
	if err := Setup(&config.Config{DBName: "sqlite3", DBPath: ":memory:", MigrationsPath: "../db/db_sqlite3/migrations/"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	u, err := GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	if u.TwoFactorRequired || len(u.TwoFactorMethods) != 0 {
		t.Fatal("bootstrap administrator must not require enrollment")
	}
	return u
}

func enrollTestMethod(t *testing.T, u User, purpose, name string) (User, TwoFactorChallenge) {
	t.Helper()
	token, challenge, err := BeginTwoFactor(u, purpose)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(challenge.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u, err = CompleteTwoFactor(token, code, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CompleteTwoFactor(token, code, name); err != ErrTwoFactorExpired {
		t.Fatalf("reused enrollment: %v", err)
	}
	return u, challenge
}

func TestTwoFactorLifecycle(t *testing.T) {
	u := setupTwoFactorTest(t)
	u.TwoFactorRequired = true
	if err := PutUser(&u); err != nil {
		t.Fatal(err)
	}
	token, challenge, err := BeginTwoFactor(u, TwoFactorSetup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CompleteTwoFactor(token, "bad", "Phone"); err != ErrTwoFactorCode {
		t.Fatal(err)
	}
	fresh, _ := GetUser(u.Id)
	if len(fresh.TwoFactorMethods) != 0 {
		t.Fatal("unconfirmed authenticator activated")
	}
	code, _ := totp.GenerateCode(challenge.Secret, time.Now())
	u, err = CompleteTwoFactor(token, code, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DeleteTwoFactorMethod(u, u.TwoFactorMethods[0].ID); err != ErrTwoFactorLastMethod {
		t.Fatal("removed required last method", err)
	}
	token, _, err = BeginTwoFactor(u, TwoFactorLogin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CompleteTwoFactor(token, code, ""); err != ErrTwoFactorCode {
		t.Fatal("accepted enrollment code replay", err)
	}
	code, _ = totp.GenerateCode(challenge.Secret, time.Now().Add(30*time.Second))
	u, err = CompleteTwoFactor(token, code, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CompleteTwoFactor(token, code, ""); err != ErrTwoFactorExpired {
		t.Fatal("replayed login", err)
	}
	data, _ := json.Marshal(u)
	if strings.Contains(string(data), challenge.Secret) || strings.Contains(string(data), "last_used_step") {
		t.Fatal("serialized secret or replay counter")
	}
	stale := u
	u, _ = enrollTestMethod(t, u, TwoFactorEnroll, "Backup phone")
	if err = PutUser(&stale); err != ErrTwoFactorExpired {
		t.Fatal("stale save overwrote 2FA state", err)
	}
	if _, err = DeleteTwoFactorMethod(u, 99999); err == nil {
		t.Fatal("deleted nonexistent method")
	}
	u, err = DeleteTwoFactorMethod(u, u.TwoFactorMethods[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = GetUser(u.Id)
	if len(u.TwoFactorMethods) != 1 {
		t.Fatal("unexpected remaining methods")
	}
	token, _, err = BeginTwoFactor(u, TwoFactorLogin)
	if err != nil {
		t.Fatal(err)
	}
	version := u.TwoFactorVersion
	if err = ResetTwoFactor(u.Id); err != nil {
		t.Fatal(err)
	}
	u, _ = GetUser(u.Id)
	if !u.TwoFactorRequired || len(u.TwoFactorMethods) != 0 || u.TwoFactorVersion <= version {
		t.Fatal("reset lost policy or failed to invalidate sessions")
	}
	if _, _, err = GetTwoFactorChallenge(token); err != ErrTwoFactorExpired {
		t.Fatal("reset left pending challenge usable")
	}
	enrollTestMethod(t, u, TwoFactorSetup, "Replacement")
}

func TestTwoFactorAttemptsAndExpiry(t *testing.T) {
	u := setupTwoFactorTest(t)
	token, _, err := BeginTwoFactor(u, TwoFactorEnroll)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, err = CompleteTwoFactor(token, "bad", "Phone")
		if i < 4 && err != ErrTwoFactorCode {
			t.Fatal(err)
		}
		if i == 4 && err != ErrTwoFactorRateLimit {
			t.Fatal("missing rate limit", err)
		}
		if i == 2 {
			// Starting a new browser/password flow must not reset attempts.
			token, _, err = BeginTwoFactor(u, TwoFactorEnroll)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, _, err = BeginTwoFactor(u, TwoFactorEnroll); err != ErrTwoFactorRateLimit {
		t.Fatal("new challenge bypassed account lockout", err)
	}
	db.Model(&User{}).Where("id = ?", u.Id).UpdateColumn("two_factor_blocked_until", time.Now().Add(-time.Second).Unix())
	token, _, err = BeginTwoFactor(u, TwoFactorEnroll)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&TwoFactorChallenge{}).Where("id = ?", digest(token)).UpdateColumn("expires_at", time.Now().Add(-time.Second).Unix())
	if _, err = CompleteTwoFactor(token, "123456", "Phone"); err != ErrTwoFactorExpired {
		t.Fatal("expired challenge accepted", err)
	}
}

func TestTwoFactorConcurrentRemovalAndOwnership(t *testing.T) {
	u := setupTwoFactorTest(t)
	u.TwoFactorRequired = true
	if err := PutUser(&u); err != nil {
		t.Fatal(err)
	}
	u, _ = enrollTestMethod(t, u, TwoFactorSetup, "First")
	u, _ = enrollTestMethod(t, u, TwoFactorEnroll, "Second")
	other := User{Username: "other", ApiKey: auth.GenerateSecureKey(32)}
	if err := PutUser(&other); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteTwoFactorMethod(other, u.TwoFactorMethods[0].ID); err == nil {
		t.Fatal("cross-user deletion allowed")
	}
	var wg sync.WaitGroup
	for _, m := range u.TwoFactorMethods {
		wg.Add(1)
		go func(id int64) { defer wg.Done(); DeleteTwoFactorMethod(u, id) }(m.ID)
	}
	wg.Wait()
	fresh, err := GetUser(u.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.TwoFactorMethods) != 1 {
		t.Fatal("concurrent deletion removed last required method")
	}
	fresh.TwoFactorRequired = false
	if err = PutUser(&fresh); err != nil {
		t.Fatal(err)
	}
	if _, err = DeleteTwoFactorMethod(fresh, fresh.TwoFactorMethods[0].ID); err != nil {
		t.Fatal("optional last method cannot be removed", err)
	}
}

func TestTwoFactorChallengeInvalidation(t *testing.T) {
	for _, change := range []string{"password", "lock", "policy", "cancel"} {
		t.Run(change, func(t *testing.T) {
			u := setupTwoFactorTest(t)
			token, _, err := BeginTwoFactor(u, TwoFactorEnroll)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "password":
				u.Hash = "changed"
			case "lock":
				u.AccountLocked = true
			case "policy":
				u.TwoFactorRequired = true
			case "cancel":
				err = CancelTwoFactor(token)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = PutUser(&u); err != nil {
				t.Fatal(err)
			}
			if _, _, err = GetTwoFactorChallenge(token); err != ErrTwoFactorExpired {
				t.Fatal("challenge still usable", err)
			}
		})
	}
}

func TestTwoFactorMigrationRollback(t *testing.T) {
	u := setupTwoFactorTest(t)
	mc := &goose.DBConf{MigrationsDir: "../db/db_sqlite3/migrations/", Env: "production", Driver: chooseDBDriver("sqlite3", ":memory:")}
	if err := goose.RunMigrationsOnDb(mc, mc.MigrationsDir, 20260612000000, db.DB()); err != nil {
		t.Fatal(err)
	}
	var username, apiKey string
	if err := db.DB().QueryRow("SELECT username, api_key FROM users WHERE id = ?", u.Id).Scan(&username, &apiKey); err != nil {
		t.Fatal(err)
	}
	if username != u.Username || apiKey != u.ApiKey {
		t.Fatal("rollback lost existing user data")
	}
	if err := goose.RunMigrationsOnDb(mc, mc.MigrationsDir, 20260924000000, db.DB()); err != nil {
		t.Fatal(err)
	}
	fresh, err := GetUser(u.Id)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.TwoFactorRequired || len(fresh.TwoFactorMethods) != 0 {
		t.Fatal("upgraded legacy account requires enrollment")
	}
}
