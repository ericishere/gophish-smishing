package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gophish/gophish/config"
	"github.com/gophish/gophish/models"
	"github.com/gophish/gophish/worker"
)

type testContext struct {
	apiKey    string
	config    *config.Config
	apiServer *Server
	admin     models.User
}

func setupTest(t *testing.T) *testContext {
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         ":memory:",
		MigrationsPath: "../../db/db_sqlite3/migrations/",
	}
	err := models.Setup(conf)
	if err != nil {
		t.Fatalf("Failed creating database: %v", err)
	}
	ctx := &testContext{}
	ctx.config = conf
	// Get the API key to use for these tests
	u, err := models.GetUser(1)
	if err != nil {
		t.Fatalf("error getting admin user: %v", err)
	}
	ctx.apiKey = u.ApiKey
	ctx.admin = u
	ctx.apiServer = NewServer(WithWorker(&mockWorker{}))
	return ctx
}

func createTestData(t *testing.T) {
	// Add a group
	group := models.Group{Name: "Test Group"}
	group.Targets = []models.Target{
		models.Target{BaseRecipient: models.BaseRecipient{Email: "test1@example.com", FirstName: "First", LastName: "Example"}},
		models.Target{BaseRecipient: models.BaseRecipient{Email: "test2@example.com", FirstName: "Second", LastName: "Example"}},
	}
	group.UserId = 1
	models.PostGroup(&group)

	// Add a template
	template := models.Template{Name: "Test Template"}
	template.Subject = "Test subject"
	template.Text = "Text text"
	template.HTML = "<html>Test</html>"
	template.UserId = 1
	models.PostTemplate(&template)

	// Add a landing page
	p := models.Page{Name: "Test Page"}
	p.HTML = "<html>Test</html>"
	p.UserId = 1
	models.PostPage(&p)

	// Add a sending profile
	smtp := models.SMTP{Name: "Test Page"}
	smtp.UserId = 1
	smtp.Host = "example.com"
	smtp.FromAddress = "test@test.com"
	models.PostSMTP(&smtp)

	// Setup and "launch" our campaign
	// Set the status such that no emails are attempted
	c := models.Campaign{Name: "Test campaign"}
	c.UserId = 1
	c.Template = template
	c.Page = p
	c.SMTP = smtp
	c.Groups = []models.Group{group}
	models.PostCampaign(&c, c.UserId)
	c.UpdateStatus(models.CampaignEmailsSent)
}

func TestSiteImportBaseHref(t *testing.T) {
	ctx := setupTest(t)
	h := "<html><head></head><body><img src=\"/test.png\"/></body></html>"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, h)
	}))
	expected := fmt.Sprintf("<html><head><base href=\"%s\"/></head><body><img src=\"/test.png\"/>\n</body></html>", ts.URL)
	defer ts.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/import/site",
		bytes.NewBuffer([]byte(fmt.Sprintf(`
			{
				"url" : "%s",
				"include_resources" : false
			}
		`, ts.URL))))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ctx.apiServer.ImportSite(response, req)
	cs := cloneResponse{}
	err := json.NewDecoder(response.Body).Decode(&cs)
	if err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	if cs.HTML != expected {
		t.Fatalf("unexpected response received. expected %s got %s", expected, cs.HTML)
	}
}

// mockWorker is a worker mock that records calls but doesn't send emails
type mockWorker struct {
	lastSendCampaign models.Campaign
	lastSendRIds     []string
	sendCalled       bool
}

func (m *mockWorker) Start() {}

func (m *mockWorker) LaunchCampaign(c models.Campaign) {}

func (m *mockWorker) SendTestEmail(s *models.EmailRequest) error {
	return nil
}

func (m *mockWorker) SendSelectedTargets(c models.Campaign, rids []string) error {
	m.sendCalled = true
	m.lastSendCampaign = c
	m.lastSendRIds = rids
	return nil
}

// Verify mockWorker implements the Worker interface
var _ worker.Worker = &mockWorker{}

func TestCampaignExportTargetsCSV(t *testing.T) {
	ctx := setupTest(t)
	createTestData(t)

	// Create a campaign with a URL for export testing
	campaign := models.Campaign{Name: "Export Test Campaign"}
	campaign.UserId = 1
	campaign.Template = models.Template{Name: "Test Template"}
	campaign.Page = models.Page{Name: "Test Page"}
	campaign.SMTP = models.SMTP{Name: "Test Page"}
	campaign.Groups = []models.Group{models.Group{Name: "Test Group"}}
	campaign.URL = "http://example.com"
	models.PostCampaign(&campaign, campaign.UserId)
	campaign.UpdateStatus(models.CampaignEmailsSent)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/campaigns/%d/targets.csv", campaign.Id),
		nil)
	req.Header.Set("Authorization", "Bearer "+ctx.apiKey)
	response := httptest.NewRecorder()
	ctx.apiServer.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}

	// Verify it's a CSV
	contentType := response.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/csv") {
		t.Fatalf("expected CSV content type, got %s", contentType)
	}

	// Parse the CSV and verify it has correct structure
	reader := csv.NewReader(response.Body)
	records, err := reader.ReadAll()
	if err != nil && err != io.EOF {
		t.Fatalf("error reading CSV: %v", err)
	}

	// First row should be the header
	if len(records) < 2 {
		t.Fatalf("expected at least 2 rows (header + 1 data row), got %d", len(records))
	}

	expectedHeaders := []string{"Email", "First Name", "Last Name", "Position", "Recipient ID (rid)", "Status", "Unique URL", "Tracking URL", "Send Date", "Reported"}
	if len(records[0]) != len(expectedHeaders) {
		t.Fatalf("expected %d CSV columns, got %d", len(expectedHeaders), len(records[0]))
	}
	for i, h := range expectedHeaders {
		if records[0][i] != h {
			t.Fatalf("expected header %q at index %d, got %q", h, i, records[0][i])
		}
	}

	// Verify data row has valid email
	if records[1][0] != "test1@example.com" && records[1][0] != "test2@example.com" {
		t.Fatalf("unexpected email in first data row: %s", records[1][0])
	}

	// Verify URL column contains the campaign URL with rid parameter
	if !strings.Contains(records[1][6], "http://example.com") {
		t.Fatalf("expected URL to contain campaign URL, got %s", records[1][6])
	}
	if !strings.Contains(records[1][6], "rid=") {
		t.Fatalf("expected URL to contain rid parameter, got %s", records[1][6])
	}
}

func TestCampaignSendTargets(t *testing.T) {
	ctx := setupTest(t)
	createTestData(t)

	// Create a manual send campaign
	campaign := models.Campaign{Name: "Manual Send Test"}
	campaign.UserId = 1
	campaign.Template = models.Template{Name: "Test Template"}
	campaign.Page = models.Page{Name: "Test Page"}
	campaign.SMTP = models.SMTP{Name: "Test Page"}
	campaign.Groups = []models.Group{models.Group{Name: "Test Group"}}
	campaign.URL = "http://example.com"
	campaign.ManualSend = true
	models.PostCampaign(&campaign, campaign.UserId)

	// Verify campaign is in Created status
	if campaign.Status != models.CampaignCreated {
		t.Fatalf("expected status %q, got %q", models.CampaignCreated, campaign.Status)
	}

	// Test sending to all targets (empty rids)
	body := []byte(`{"rids": []}`)
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/campaigns/%d/send", campaign.Id),
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ctx.apiKey)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ctx.apiServer.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}

	// Verify the mock worker was called
	mw := ctx.apiServer.worker.(*mockWorker)
	if !mw.sendCalled {
		t.Fatalf("expected SendSelectedTargets to be called on the worker")
	}

	// Test sending to specific targets
	mw.sendCalled = false
	body = []byte(`{"rids": ["abc123", "def456"]}`)
	req = httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/campaigns/%d/send", campaign.Id),
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ctx.apiKey)
	req.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	ctx.apiServer.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}

	if !mw.sendCalled {
		t.Fatalf("expected SendSelectedTargets to be called for specific rids")
	}
	if len(mw.lastSendRIds) != 2 {
		t.Fatalf("expected 2 rids, got %d", len(mw.lastSendRIds))
	}
}

func TestManualSendCampaignDoesNotAutoSend(t *testing.T) {
	setupTest(t)
	createTestData(t)

	// Create a manual send campaign
	campaign := models.Campaign{Name: "No Auto Send Test"}
	campaign.UserId = 1
	campaign.Template = models.Template{Name: "Test Template"}
	campaign.Page = models.Page{Name: "Test Page"}
	campaign.SMTP = models.SMTP{Name: "Test Page"}
	campaign.Groups = []models.Group{models.Group{Name: "Test Group"}}
	campaign.URL = "http://example.com"
	campaign.ManualSend = true
	models.PostCampaign(&campaign, campaign.UserId)

	// Verify campaign status is Created
	if campaign.Status != models.CampaignCreated {
		t.Fatalf("expected status %q, got %q", models.CampaignCreated, campaign.Status)
	}

	// Verify all maillogs have far-future send dates
	ms, err := models.GetMailLogsByCampaign(campaign.Id)
	if err != nil {
		t.Fatalf("error getting maillogs: %v", err)
	}

	for _, m := range ms {
		if !m.SendDate.Equal(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("expected sentinel date 2099-01-01, got %v for maillog %d", m.SendDate, m.Id)
		}
		if m.Processing {
			t.Fatalf("expected processing=false for manual send maillog %d", m.Id)
		}
	}
}
