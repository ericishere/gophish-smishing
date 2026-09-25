package auth

import (
	"encoding/base32"
	"testing"
	"time"
)

func TestTOTPInteroperability(t *testing.T) {
	// RFC 6238 Appendix B SHA-1 vectors, reduced to Google's six digits.
	// This is the public ASCII test key from the RFC, not an account secret.
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	vectors := []struct {
		unix int64
		code string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	}
	for _, v := range vectors {
		step, ok := MatchTOTP(secret, v.code, time.Unix(v.unix, 0), -1)
		if !ok || step != v.unix/30 {
			t.Fatalf("RFC vector at %d failed", v.unix)
		}
		if _, ok = MatchTOTP(secret, v.code, time.Unix(v.unix, 0), step); ok {
			t.Fatal("accepted a reused code")
		}
	}
	for _, invalid := range []string{"", "28708", "0287082", " 287082", "287082 ", "abcdef"} {
		if _, ok := MatchTOTP(secret, invalid, time.Unix(59, 0), -1); ok {
			t.Errorf("accepted malformed code %q", invalid)
		}
	}
	if _, ok := MatchTOTP(secret, "287082", time.Unix(89, 0), -1); !ok {
		t.Fatal("rejected one-step drift")
	}
	if _, ok := MatchTOTP(secret, "287082", time.Unix(119, 0), -1); ok {
		t.Fatal("accepted expired code")
	}
}

func TestTOTPProvisioning(t *testing.T) {
	original, err := NewTOTPKey("user+test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := TOTPKey("user+test@example.com", original.Secret())
	if err != nil {
		t.Fatal(err)
	}
	if restored.URL() != original.URL() {
		t.Fatal("QR provisioning changed the secret or parameters")
	}
	if restored.Issuer() != "Gophish" || restored.Period() != 30 || restored.Digits().Length() != 6 {
		t.Fatal("incompatible provisioning parameters")
	}
	if _, err := restored.Image(240, 240); err != nil {
		t.Fatal(err)
	}
}
