package controllers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gophish/gophish/models"
	"github.com/gorilla/handlers"
)

func TestPhishingEventsDiscardSubmittedFields(t *testing.T) {
	ctx := setupTest(t)
	defer tearDown(t, ctx)
	campaign := getFirstCampaign(t)
	rid := campaign.Results[0].RId

	// Capture the actual outgoing event, not just the database representation.
	deliveries := make(chan []byte, 8)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		deliveries <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sink.Close()
	wh := models.Webhook{Name: "Privacy test", URL: sink.URL, IsActive: true}
	if err := models.PostWebhook(&wh); err != nil {
		t.Fatal(err)
	}
	defer models.DeleteWebhook(wh.Id)

	for _, test := range []struct{ method, path, event string }{
		{"POST", "/", models.EventDataSubmit},
		{"GET", "/", models.EventClicked},
		{"GET", "/track", models.EventOpened},
		{"GET", "/report", models.EventReported},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			values := url.Values{"username": {"private-user"}, "password": {"private-password"},
				"custom_account": {"private-alias"}}
			endpoint := fmt.Sprintf("%s%s?%s=%s&query_secret=private-query", ctx.phishServer.URL, test.path, models.RecipientParameter, rid)
			var resp *http.Response
			var err error
			if test.method == "POST" {
				resp, err = http.PostForm(endpoint, values)
			} else {
				resp, err = http.Get(endpoint + "&" + values.Encode())
			}
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				t.Fatalf("request failed: %d", resp.StatusCode)
			}
			select {
			case body := <-deliveries:
				var event models.Event
				if err := json.Unmarshal(body, &event); err != nil {
					t.Fatal(err)
				}
				if event.Message != test.event {
					t.Fatalf("expected %s, got %s", test.event, event.Message)
				}
				assertNoSubmittedFields(t, event.Details)
			case <-time.After(5 * time.Second):
				t.Fatal("webhook event not delivered")
			}
			stored := getFirstCampaign(t)
			found := false
			for _, event := range stored.Events {
				if event.Details != "" {
					assertNoSubmittedFields(t, event.Details)
				}
				found = found || event.Message == test.event
			}
			if !found {
				t.Fatalf("submission tracking lost: %s", test.event)
			}
		})
	}
}

func assertNoSubmittedFields(t *testing.T, details string) {
	t.Helper()
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(details), &data); err != nil {
		t.Fatal(err)
	}
	if _, exists := data["payload"]; exists || strings.Contains(details, "private-") {
		t.Fatalf("submitted fields retained: %s", details)
	}
	if _, exists := data["browser"]; !exists {
		t.Fatal("browser metadata lost")
	}
}

func TestPhishingAccessLogExcludesFormValues(t *testing.T) {
	var output bytes.Buffer
	handler := handlers.CustomLoggingHandler(&output, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("username") != "private-user" {
			t.Fatal("logging changed handler input")
		}
		w.WriteHeader(http.StatusNoContent)
	}), logPhishingRequest)
	req := httptest.NewRequest("GET", "/landing?username=private-user&password=private-password", nil)
	req.Header.Set("Referer", "https://example.com/?secret=private-referrer")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "?") {
		t.Fatalf("form values leaked to access log: %s", output.String())
	}
	if !strings.Contains(output.String(), "GET /landing") || !strings.Contains(output.String(), "204") {
		t.Fatalf("missing operational log data: %s", output.String())
	}
}
