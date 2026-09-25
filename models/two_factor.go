package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gophish/gophish/auth"
	"github.com/jinzhu/gorm"
)

const (
	TwoFactorLogin      = "login"
	TwoFactorSetup      = "setup"
	TwoFactorEnroll     = "enroll"
	maxTwoFactorMethods = 10
)

var (
	ErrTwoFactorExpired    = errors.New("This verification has expired. Please sign in again")
	ErrTwoFactorCode       = errors.New("Invalid or already used code. Enter a fresh six-digit code")
	ErrTwoFactorRateLimit  = errors.New("Too many verification attempts. Please wait five minutes and try again")
	ErrTwoFactorLastMethod = errors.New("2FA is required. Add another method before deleting this one")
	ErrTwoFactorName       = errors.New("Enter a method name of 1 to 64 characters")
	ErrTwoFactorLimit      = errors.New("You can add up to 10 authenticator methods")
)

// TwoFactorMethod contains only confirmed authenticators. Secrets and replay
// counters must never be serialized in user/API responses.
type TwoFactorMethod struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"-"`
	Name         string    `json:"name"`
	Secret       string    `json:"-"`
	LastUsedStep int64     `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// TwoFactorChallenge is a single-use, ten-minute password-authenticated flow.
// Only the random token is placed in the encrypted browser session; the DB
// stores its digest. Starting a new flow replaces the user's previous flow.
type TwoFactorChallenge struct {
	ID                  string `json:"-"`
	UserID              int64  `json:"-"`
	Version             int64  `json:"-"`
	PasswordFingerprint string `json:"-"`
	Purpose             string `json:"-"`
	Secret              string `json:"-"`
	ExpiresAt           int64  `json:"-"`
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// lockTwoFactor serializes changes for one account on both SQLite and MySQL.
// The no-op UPDATE acquires a write lock before reading any security state.
func lockTwoFactor(tx *gorm.DB, id int64) (User, error) {
	var u User
	err := tx.Model(&User{}).Where("id = ?", id).
		UpdateColumn("two_factor_version", gorm.Expr("two_factor_version")).Error
	if err != nil {
		return u, err
	}
	err = tx.Preload("Role").Preload("TwoFactorMethods").First(&u, id).Error
	return u, err
}

func saveUser(u *User) error {
	if u.Id == 0 {
		return db.Omit("TwoFactorMethods").Create(u).Error
	}
	tx := db.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback()
	existing, err := lockTwoFactor(tx, u.Id)
	if err != nil {
		return err
	}
	// Prevent a stale user snapshot from undoing a security change.
	if existing.TwoFactorVersion != u.TwoFactorVersion {
		return ErrTwoFactorExpired
	}
	if existing.Hash != u.Hash || existing.AccountLocked != u.AccountLocked ||
		existing.TwoFactorRequired != u.TwoFactorRequired {
		u.TwoFactorVersion++
		if err = tx.Where("user_id = ?", u.Id).Delete(&TwoFactorChallenge{}).Error; err != nil {
			return err
		}
	}
	err = tx.Omit("TwoFactorMethods", "TwoFactorFailures", "TwoFactorBlockedUntil").Save(u).Error
	if err != nil {
		return err
	}
	return tx.Commit().Error
}

// BeginTwoFactor must only be called after verifying the current password (or
// an administrator's authenticated impersonation request).
func BeginTwoFactor(user User, purpose string) (string, TwoFactorChallenge, error) {
	challenge := TwoFactorChallenge{}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", challenge, err
	}
	token := hex.EncodeToString(tokenBytes)
	tx := db.Begin()
	if tx.Error != nil {
		return "", challenge, tx.Error
	}
	defer tx.Rollback()
	u, err := lockTwoFactor(tx, user.Id)
	if err != nil {
		return "", challenge, err
	}
	if u.AccountLocked || u.Hash != user.Hash || u.TwoFactorVersion != user.TwoFactorVersion {
		return "", challenge, ErrTwoFactorExpired
	}
	if u.TwoFactorBlockedUntil > time.Now().Unix() {
		return "", challenge, ErrTwoFactorRateLimit
	}
	switch purpose {
	case TwoFactorLogin:
		if len(u.TwoFactorMethods) == 0 {
			return "", challenge, ErrTwoFactorExpired
		}
	case TwoFactorSetup:
		if !u.TwoFactorRequired || len(u.TwoFactorMethods) != 0 {
			return "", challenge, ErrTwoFactorExpired
		}
	case TwoFactorEnroll:
		if len(u.TwoFactorMethods) >= maxTwoFactorMethods {
			return "", challenge, ErrTwoFactorLimit
		}
	default:
		return "", challenge, ErrTwoFactorExpired
	}
	if purpose != TwoFactorLogin {
		key, err := auth.NewTOTPKey(u.Username)
		if err != nil {
			return "", challenge, err
		}
		challenge.Secret = key.Secret()
	}
	challenge.ID = digest(token)
	challenge.UserID = u.Id
	challenge.Version = u.TwoFactorVersion
	challenge.PasswordFingerprint = digest(u.Hash)
	challenge.Purpose = purpose
	challenge.ExpiresAt = time.Now().Add(10 * time.Minute).Unix()
	if err = tx.Where("user_id = ? OR expires_at < ?", u.Id, time.Now().Unix()).Delete(&TwoFactorChallenge{}).Error; err != nil {
		return "", challenge, err
	}
	if err = tx.Create(&challenge).Error; err != nil {
		return "", challenge, err
	}
	return token, challenge, tx.Commit().Error
}

func readTwoFactorChallenge(tx *gorm.DB, token string) (TwoFactorChallenge, User, error) {
	var challenge TwoFactorChallenge
	var u User
	if len(token) != 64 {
		return challenge, u, ErrTwoFactorExpired
	}
	err := tx.Where("id = ? AND expires_at > ?", digest(token), time.Now().Unix()).First(&challenge).Error
	if err != nil {
		return challenge, u, ErrTwoFactorExpired
	}
	err = tx.Preload("Role").Preload("TwoFactorMethods").First(&u, challenge.UserID).Error
	if err != nil {
		return challenge, u, err
	}
	if u.AccountLocked || u.TwoFactorVersion != challenge.Version || digest(u.Hash) != challenge.PasswordFingerprint {
		return challenge, u, ErrTwoFactorExpired
	}
	return challenge, u, nil
}

// GetTwoFactorChallenge is used only to render an in-progress enrollment or
// challenge. It does not grant an authenticated session.
func GetTwoFactorChallenge(token string) (TwoFactorChallenge, User, error) {
	return readTwoFactorChallenge(db, token)
}

func CancelTwoFactor(token string) error {
	return db.Where("id = ?", digest(token)).Delete(&TwoFactorChallenge{}).Error
}

// CompleteTwoFactor verifies, consumes, and (for enrollment) activates a method
// in one transaction. Failed attempts persist independently of browser cookies.
func CompleteTwoFactor(token, code, name string) (User, error) {
	challenge, _, err := GetTwoFactorChallenge(token)
	if err != nil {
		return User{}, err
	}
	tx := db.Begin()
	if tx.Error != nil {
		return User{}, tx.Error
	}
	defer tx.Rollback()
	u, err := lockTwoFactor(tx, challenge.UserID)
	if err != nil {
		return u, err
	}
	challenge, u, err = readTwoFactorChallenge(tx, token)
	if err != nil {
		return u, err
	}
	now := time.Now()
	if u.TwoFactorBlockedUntil > now.Unix() {
		return u, ErrTwoFactorRateLimit
	}
	if challenge.Purpose != TwoFactorLogin {
		name = strings.TrimSpace(name)
		if len(name) == 0 || utf8.RuneCountInString(name) > 64 {
			return u, ErrTwoFactorName
		}
		if len(u.TwoFactorMethods) >= maxTwoFactorMethods {
			return u, ErrTwoFactorLimit
		}
	}
	var matched bool
	var step int64
	var methodID int64
	if challenge.Purpose == TwoFactorLogin {
		for _, method := range u.TwoFactorMethods {
			step, matched = auth.MatchTOTP(method.Secret, code, now, method.LastUsedStep)
			if matched {
				methodID = method.ID
				break
			}
		}
	} else {
		step, matched = auth.MatchTOTP(challenge.Secret, code, now, -1)
	}
	if !matched {
		if u.TwoFactorBlockedUntil != 0 {
			u.TwoFactorFailures = 0
		}
		u.TwoFactorFailures++
		u.TwoFactorBlockedUntil = 0
		if u.TwoFactorFailures >= 5 {
			u.TwoFactorBlockedUntil = now.Add(5 * time.Minute).Unix()
		}
		err = tx.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]interface{}{
			"two_factor_failures": u.TwoFactorFailures, "two_factor_blocked_until": u.TwoFactorBlockedUntil,
		}).Error
		if err != nil {
			return u, err
		}
		if err = tx.Commit().Error; err != nil {
			return u, err
		}
		if u.TwoFactorBlockedUntil != 0 {
			return u, ErrTwoFactorRateLimit
		}
		return u, ErrTwoFactorCode
	}
	if challenge.Purpose == TwoFactorLogin {
		err = tx.Model(&TwoFactorMethod{}).Where("id = ?", methodID).UpdateColumn("last_used_step", step).Error
	} else {
		method := TwoFactorMethod{UserID: u.Id, Name: name, Secret: challenge.Secret, LastUsedStep: step, CreatedAt: now.UTC()}
		err = tx.Create(&method).Error
		u.TwoFactorVersion++
		u.TwoFactorMethods = append(u.TwoFactorMethods, method)
	}
	if err != nil {
		return u, err
	}
	err = tx.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]interface{}{
		"two_factor_version": u.TwoFactorVersion, "two_factor_failures": 0, "two_factor_blocked_until": 0,
	}).Error
	if err != nil {
		return u, err
	}
	if err = tx.Where("user_id = ?", u.Id).Delete(&TwoFactorChallenge{}).Error; err != nil {
		return u, err
	}
	return u, tx.Commit().Error
}

// DeleteTwoFactorMethod enforces ownership and the required-last-method rule
// inside the same transaction as the deletion, including concurrent requests.
func DeleteTwoFactorMethod(user User, methodID int64) (User, error) {
	tx := db.Begin()
	if tx.Error != nil {
		return user, tx.Error
	}
	defer tx.Rollback()
	u, err := lockTwoFactor(tx, user.Id)
	if err != nil {
		return u, err
	}
	if user.TwoFactorVersion != u.TwoFactorVersion || u.AccountLocked {
		return u, ErrTwoFactorExpired
	}
	var method TwoFactorMethod
	if err = tx.Where("id = ? AND user_id = ?", methodID, u.Id).First(&method).Error; err != nil {
		return u, err
	}
	if u.TwoFactorRequired && len(u.TwoFactorMethods) <= 1 {
		return u, ErrTwoFactorLastMethod
	}
	if err = tx.Delete(&method).Error; err != nil {
		return u, err
	}
	if err = invalidateTwoFactor(tx, &u); err != nil {
		return u, err
	}
	return u, tx.Commit().Error
}

func invalidateTwoFactor(tx *gorm.DB, u *User) error {
	u.TwoFactorVersion++
	if err := tx.Model(&User{}).Where("id = ?", u.Id).UpdateColumn("two_factor_version", u.TwoFactorVersion).Error; err != nil {
		return err
	}
	return tx.Where("user_id = ?", u.Id).Delete(&TwoFactorChallenge{}).Error
}

// ResetTwoFactor preserves the administrator's requirement while invalidating
// every browser session and pending enrollment for the user.
func ResetTwoFactor(id int64) error {
	tx := db.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback()
	u, err := lockTwoFactor(tx, id)
	if err != nil {
		return err
	}
	if err = tx.Where("user_id = ?", id).Delete(&TwoFactorMethod{}).Error; err != nil {
		return err
	}
	if err = invalidateTwoFactor(tx, &u); err != nil {
		return err
	}
	if err = tx.Model(&User{}).Where("id = ?", id).Updates(map[string]interface{}{
		"two_factor_failures": 0, "two_factor_blocked_until": 0,
	}).Error; err != nil {
		return err
	}
	return tx.Commit().Error
}

// RecordLogin avoids saving a stale copy of the user's authentication settings.
func RecordLogin(id int64) error {
	return db.Model(&User{}).Where("id = ?", id).UpdateColumn("last_login", time.Now().UTC()).Error
}
