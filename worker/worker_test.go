package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gophish/gophish/config"
	"github.com/gophish/gophish/mailer"
	"github.com/gophish/gophish/models"
)

type logMailer struct {
	queue chan []mailer.Mail
}

func (m *logMailer) Start(ctx context.Context) {}

func (m *logMailer) Queue(ms []mailer.Mail) {
	m.queue <- ms
}

// testContext is context to cover API related functions
type testContext struct {
	config *config.Config
}

func setupTest(t *testing.T) *testContext {
	conf := &config.Config{
		DBName:         "sqlite3",
		DBPath:         ":memory:",
		MigrationsPath: "../db/db_sqlite3/migrations/",
	}
	err := models.Setup(conf)
	if err != nil {
		t.Fatalf("Failed creating database: %v", err)
	}
	ctx := &testContext{}
	ctx.config = conf
	createTestData(t, ctx)
	return ctx
}

func createTestData(t *testing.T, ctx *testContext) {
	ctx.config.TestFlag = true
	// Add a group
	group := models.Group{Name: "Test Group"}
	for i := 0; i < 10; i++ {
		group.Targets = append(group.Targets, models.Target{
			BaseRecipient: models.BaseRecipient{
				Email:     fmt.Sprintf("test%d@example.com", i),
				FirstName: "First",
				LastName:  "Example"}})
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
}

func setupCampaign(id int) (*models.Campaign, error) {
	// Setup and "launch" our campaign
	// Set the status such that no emails are attempted
	c := models.Campaign{Name: fmt.Sprintf("Test campaign - %d", id)}
	c.UserId = 1
	template, err := models.GetTemplate(1, 1)
	if err != nil {
		return nil, err
	}
	c.Template = template

	page, err := models.GetPage(1, 1)
	if err != nil {
		return nil, err
	}
	c.Page = page

	smtp, err := models.GetSMTP(1, 1)
	if err != nil {
		return nil, err
	}
	c.SMTP = smtp

	group, err := models.GetGroup(1, 1)
	if err != nil {
		return nil, err
	}
	c.Groups = []models.Group{group}
	err = models.PostCampaign(&c, c.UserId)
	if err != nil {
		return nil, err
	}
	err = c.UpdateStatus(models.CampaignEmailsSent)
	return &c, err
}

func setupManualSendCampaign(id int) (*models.Campaign, error) {
	c := models.Campaign{Name: fmt.Sprintf("Manual campaign - %d", id)}
	c.UserId = 1
	c.ManualSend = true
	template, err := models.GetTemplate(1, 1)
	if err != nil {
		return nil, err
	}
	c.Template = template

	page, err := models.GetPage(1, 1)
	if err != nil {
		return nil, err
	}
	c.Page = page

	smtp, err := models.GetSMTP(1, 1)
	if err != nil {
		return nil, err
	}
	c.SMTP = smtp

	group, err := models.GetGroup(1, 1)
	if err != nil {
		return nil, err
	}
	c.Groups = []models.Group{group}
	err = models.PostCampaign(&c, c.UserId)
	if err != nil {
		return nil, err
	}
	return &c, err
}

func TestSendSelectedTargets(t *testing.T) {
	setupTest(t)

	campaign, err := setupManualSendCampaign(1)
	if err != nil {
		t.Fatalf("error creating campaign: %v", err)
	}

	// Verify campaign is in Created status (not auto-sent)
	if campaign.Status != models.CampaignCreated {
		t.Fatalf("expected campaign status %q, got %q", models.CampaignCreated, campaign.Status)
	}

	// Get all maillogs and pick the first two rids
	allMs, err := models.GetMailLogsByCampaign(campaign.Id)
	if err != nil {
		t.Fatalf("error getting maillogs: %v", err)
	}
	if len(allMs) < 2 {
		t.Fatalf("expected at least 2 maillogs, got %d", len(allMs))
	}

	selectedRids := []string{allMs[0].RId, allMs[1].RId}

	lm := &logMailer{queue: make(chan []mailer.Mail, 10)}
	worker := &DefaultWorker{}
	worker.mailer = lm

	// Send to selected targets
	err = worker.SendSelectedTargets(*campaign, selectedRids)
	if err != nil {
		t.Fatalf("error sending selected targets: %v", err)
	}

	// Verify that the mailer received only the selected maillogs
	select {
	case ms := <-lm.queue:
		if len(ms) != len(selectedRids) {
			t.Fatalf("expected %d emails queued, got %d", len(selectedRids), len(ms))
		}
		for _, m := range ms {
			ml, ok := m.(*models.MailLog)
			if !ok {
				t.Fatalf("unable to cast mail to models.MailLog")
			}
			found := false
			for _, rid := range selectedRids {
				if ml.RId == rid {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("unexpected maillog rid %q - not in selected rids", ml.RId)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for mailer queue")
	}
}

func TestSendSelectedTargetsAll(t *testing.T) {
	setupTest(t)

	campaign, err := setupManualSendCampaign(1)
	if err != nil {
		t.Fatalf("error creating campaign: %v", err)
	}

	// Get all maillogs
	allMs, err := models.GetMailLogsByCampaign(campaign.Id)
	if err != nil {
		t.Fatalf("error getting maillogs: %v", err)
	}

	lm := &logMailer{queue: make(chan []mailer.Mail, 10)}
	worker := &DefaultWorker{}
	worker.mailer = lm

	// Send to ALL targets (empty rids)
	err = worker.SendSelectedTargets(*campaign, nil)
	if err != nil {
		t.Fatalf("error sending all targets: %v", err)
	}

	// Verify that the mailer received all maillogs
	select {
	case ms := <-lm.queue:
		if len(ms) != len(allMs) {
			t.Fatalf("expected %d emails queued, got %d", len(allMs), len(ms))
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for mailer queue")
	}
}

func TestSendSelectedTargetsNoDuplicate(t *testing.T) {
	setupTest(t)

	campaign, err := setupManualSendCampaign(1)
	if err != nil {
		t.Fatalf("error creating campaign: %v", err)
	}

	allMs, err := models.GetMailLogsByCampaign(campaign.Id)
	if err != nil {
		t.Fatalf("error getting maillogs: %v", err)
	}

	lm := &logMailer{queue: make(chan []mailer.Mail, 10)}
	worker := &DefaultWorker{}
	worker.mailer = lm

	// First send locks and queues all maillogs.
	if err := worker.SendSelectedTargets(*campaign, nil); err != nil {
		t.Fatalf("error on first send: %v", err)
	}
	select {
	case ms := <-lm.queue:
		if len(ms) != len(allMs) {
			t.Fatalf("expected %d emails queued, got %d", len(allMs), len(ms))
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for mailer queue")
	}

	// A repeated send (e.g. a double-click or retry) must not re-queue the
	// already-locked maillogs and send duplicate emails.
	if err := worker.SendSelectedTargets(*campaign, nil); err != nil {
		t.Fatalf("error on second send: %v", err)
	}
	select {
	case ms := <-lm.queue:
		t.Fatalf("expected no emails queued on repeated send, got %d", len(ms))
	case <-time.After(500 * time.Millisecond):
		// Expected: nothing queued.
	}
}

func TestMailLogGrouping(t *testing.T) {
	setupTest(t)

	// Create the campaigns and unlock the maillogs so that they're picked up
	// by the worker
	for i := 0; i < 10; i++ {
		campaign, err := setupCampaign(i)
		if err != nil {
			t.Fatalf("error creating campaign: %v", err)
		}
		ms, err := models.GetMailLogsByCampaign(campaign.Id)
		if err != nil {
			t.Fatalf("error getting maillogs for campaign: %v", err)
		}
		for _, m := range ms {
			m.Unlock()
		}
	}

	lm := &logMailer{queue: make(chan []mailer.Mail)}
	worker := &DefaultWorker{}
	worker.mailer = lm

	// Trigger the worker, generating the maillogs and sending them to the
	// mailer
	worker.processCampaigns(time.Now())

	// Verify that each slice of maillogs received belong to the same campaign
	for i := 0; i < 10; i++ {
		ms := <-lm.queue
		maillog, ok := ms[0].(*models.MailLog)
		if !ok {
			t.Fatalf("unable to cast mail to models.MailLog")
		}
		expected := maillog.CampaignId
		for _, m := range ms {
			maillog, ok = m.(*models.MailLog)
			if !ok {
				t.Fatalf("unable to cast mail to models.MailLog")
			}
			got := maillog.CampaignId
			if got != expected {
				t.Fatalf("unexpected campaign ID received for maillog: got %d expected %d", got, expected)
			}
		}
	}
}
