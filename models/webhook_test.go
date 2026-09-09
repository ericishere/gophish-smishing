package models

import (
	"testing"
)

// TestWebhookValidateScheme covers the URL scheme whitelist added to
// (*Webhook).Validate: only http and https are accepted.
func TestWebhookValidateScheme(t *testing.T) {
	testCases := []struct {
		name        string
		webhook     Webhook
		expectedErr error
	}{
		{
			name:        "valid http URL",
			webhook:     Webhook{Name: "hook", URL: "http://example.com/webhook"},
			expectedErr: nil,
		},
		{
			name:        "valid https URL",
			webhook:     Webhook{Name: "hook", URL: "https://example.com/webhook"},
			expectedErr: nil,
		},
		{
			name:        "file scheme rejected",
			webhook:     Webhook{Name: "hook", URL: "file:///etc/passwd"},
			expectedErr: ErrInvalidURLScheme,
		},
		{
			name:        "ftp scheme rejected",
			webhook:     Webhook{Name: "hook", URL: "ftp://example.com/file"},
			expectedErr: ErrInvalidURLScheme,
		},
		{
			name:        "invalid URL rejected",
			webhook:     Webhook{Name: "hook", URL: "http://[::1]:namedport"},
			expectedErr: ErrInvalidURLScheme,
		},
		{
			name:        "empty URL rejected",
			webhook:     Webhook{Name: "hook", URL: ""},
			expectedErr: ErrURLNotSpecified,
		},
		{
			name:        "empty name rejected",
			webhook:     Webhook{Name: "", URL: "https://example.com/webhook"},
			expectedErr: ErrNameNotSpecified,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wh := tc.webhook
			err := wh.Validate()
			if err != tc.expectedErr {
				t.Errorf("Validate() error = %v, expected %v", err, tc.expectedErr)
			}
		})
	}
}
