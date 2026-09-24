package auth

import (
	"encoding/base32"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// NewTOTPKey creates a Google Authenticator compatible key: SHA-1, six digits,
// and a 30 second period. Key material comes from crypto/rand in otp.Generate.
func NewTOTPKey(account string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{
		Issuer: "Gophish", AccountName: account, Period: 30,
		SecretSize: 20, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
}

// TOTPKey reconstructs a provisioning key without generating new key material.
func TOTPKey(account, secret string) (*otp.Key, error) {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return nil, err
	}
	return totp.Generate(totp.GenerateOpts{
		Issuer: "Gophish", AccountName: account, Period: 30,
		Secret: raw, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
}

// MatchTOTP returns the accepted counter so the caller can atomically persist
// it and reject reuse. Allow one step of clock drift in either direction.
func MatchTOTP(secret, code string, now time.Time, lastUsed int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	counter := now.Unix() / 30
	for _, offset := range []int64{0, -1, 1} {
		step := counter + offset
		if step <= lastUsed || step < 0 {
			continue
		}
		valid, err := totp.ValidateCustom(code, secret, time.Unix(step*30, 0), totp.ValidateOpts{
			Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && valid {
			return step, true
		}
	}
	return 0, false
}
