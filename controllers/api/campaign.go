package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	ctx "github.com/gophish/gophish/context"
	log "github.com/gophish/gophish/logger"
	"github.com/gophish/gophish/models"
	"github.com/gorilla/mux"
	"github.com/jinzhu/gorm"
	"github.com/sirupsen/logrus"
)

// Campaigns returns a list of campaigns if requested via GET.
// If requested via POST, APICampaigns creates a new campaign and returns a reference to it.
func (as *Server) Campaigns(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET":
		cs, err := models.GetCampaigns(ctx.Get(r, "user_id").(int64))
		if err != nil {
			log.Error(err)
		}
		JSONResponse(w, cs, http.StatusOK)
	//POST: Create a new campaign and return it as JSON
	case r.Method == "POST":
		c := models.Campaign{}
		// Put the request into a campaign
		err := json.NewDecoder(r.Body).Decode(&c)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Invalid JSON structure"}, http.StatusBadRequest)
			return
		}
		err = models.PostCampaign(&c, ctx.Get(r, "user_id").(int64))
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
			return
		}
		// If the campaign is scheduled to launch immediately, send it to the worker.
		// Otherwise, the worker will pick it up at the scheduled time.
		// Skip automatic launching if ManualSend is enabled.
		if c.Status == models.CampaignInProgress && !c.ManualSend {
			go as.worker.LaunchCampaign(c)
		}
		JSONResponse(w, c, http.StatusCreated)
	}
}

// CampaignsSummary returns the summary for the current user's campaigns
func (as *Server) CampaignsSummary(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET":
		cs, err := models.GetCampaignSummaries(ctx.Get(r, "user_id").(int64))
		if err != nil {
			log.Error(err)
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			return
		}
		JSONResponse(w, cs, http.StatusOK)
	}
}

// Campaign returns details about the requested campaign. If the campaign is not
// valid, APICampaign returns null.
func (as *Server) Campaign(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.ParseInt(vars["id"], 0, 64)
	c, err := models.GetCampaign(id, ctx.Get(r, "user_id").(int64))
	if err != nil {
		log.Error(err)
		JSONResponse(w, models.Response{Success: false, Message: "Campaign not found"}, http.StatusNotFound)
		return
	}
	switch {
	case r.Method == "GET":
		JSONResponse(w, c, http.StatusOK)
	case r.Method == "DELETE":
		err = models.DeleteCampaign(id)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Error deleting campaign"}, http.StatusInternalServerError)
			return
		}
		JSONResponse(w, models.Response{Success: true, Message: "Campaign deleted successfully!"}, http.StatusOK)
	}
}

// CampaignResults returns just the results for a given campaign to
// significantly reduce the information returned.
func (as *Server) CampaignResults(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.ParseInt(vars["id"], 0, 64)
	cr, err := models.GetCampaignResults(id, ctx.Get(r, "user_id").(int64))
	if err != nil {
		log.Error(err)
		JSONResponse(w, models.Response{Success: false, Message: "Campaign not found"}, http.StatusNotFound)
		return
	}
	if r.Method == "GET" {
		JSONResponse(w, cr, http.StatusOK)
		return
	}
}

// CampaignSummary returns the summary for a given campaign.
func (as *Server) CampaignSummary(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.ParseInt(vars["id"], 0, 64)
	switch {
	case r.Method == "GET":
		cs, err := models.GetCampaignSummary(id, ctx.Get(r, "user_id").(int64))
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				JSONResponse(w, models.Response{Success: false, Message: "Campaign not found"}, http.StatusNotFound)
			} else {
				JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			}
			log.Error(err)
			return
		}
		JSONResponse(w, cs, http.StatusOK)
	}
}

// CampaignComplete effectively "ends" a campaign.
// Future phishing emails clicked will return a simple "404" page.
func (as *Server) CampaignComplete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.ParseInt(vars["id"], 0, 64)
	switch {
	case r.Method == "GET":
		err := models.CompleteCampaign(id, ctx.Get(r, "user_id").(int64))
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Error completing campaign"}, http.StatusInternalServerError)
			return
		}
		JSONResponse(w, models.Response{Success: true, Message: "Campaign completed successfully!"}, http.StatusOK)
	}
}

// CampaignExportTargets exports the campaign results as a CSV file containing
// comprehensive target data including email, name, position, unique URL,
// tracking URL, recipient ID, and send date.
func (as *Server) CampaignExportTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		JSONResponse(w, models.Response{Success: false, Message: "Method not allowed"}, http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	id, _ := strconv.ParseInt(vars["id"], 0, 64)
	uid := ctx.Get(r, "user_id").(int64)

	// Get the full campaign to access the URL field
	campaign, err := models.GetCampaign(id, uid)
	if err != nil {
		log.Error(err)
		JSONResponse(w, models.Response{Success: false, Message: "Campaign not found"}, http.StatusNotFound)
		return
	}

	// Get campaign results
	cr, err := models.GetCampaignResults(id, uid)
	if err != nil {
		log.Error(err)
		JSONResponse(w, models.Response{Success: false, Message: "Campaign not found"}, http.StatusNotFound)
		return
	}

	// Set response headers for CSV download
	filename := fmt.Sprintf("%s - Target URLs.csv", safeFilename(campaign.Name))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	// Create CSV writer
	writer := csv.NewWriter(w)

	// Write CSV header
	header := []string{"Email", "First Name", "Last Name", "Position", "Recipient ID (rid)", "Status", "Unique URL", "Tracking URL", "Send Date", "Reported"}
	writer.Write(header)

	// Write each result row
	for _, result := range cr.Results {
		// Generate the phishing URL and tracking URL for this target
		ptx, err := models.NewPhishingTemplateContext(&campaign, result.BaseRecipient, result.RId)
		if err != nil {
			log.WithFields(logrus.Fields{
				"campaign_id": id,
				"result_id":   result.RId,
				"error":       err,
			}).Error("Error generating template context for CSV export")
			// Still write the row with empty URLs
			ptx = models.PhishingTemplateContext{
				URL:         "",
				TrackingURL: "",
			}
		}

		reported := "No"
		if result.Reported {
			reported = "Yes"
		}

		row := []string{
			escapeCSVFormula(result.Email),
			escapeCSVFormula(result.FirstName),
			escapeCSVFormula(result.LastName),
			escapeCSVFormula(result.Position),
			escapeCSVFormula(result.RId),
			escapeCSVFormula(result.Status),
			escapeCSVFormula(normalizeExportedURL(ptx.URL)),
			escapeCSVFormula(ptx.TrackingURL),
			result.SendDate.Format("2006-01-02 15:04:05"),
			reported,
		}
		writer.Write(row)
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		log.WithFields(logrus.Fields{
			"campaign_id": id,
			"error":       err,
		}).Error("Error writing CSV export")
	}
}

// CampaignSendTargets allows manual triggering of email sending for a campaign.
// It accepts a POST request with an optional list of recipient IDs (rids).
// If rids is empty, all pending maillogs for the campaign are sent.
func (as *Server) CampaignSendTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		JSONResponse(w, models.Response{Success: false, Message: "Method not allowed"}, http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	id, _ := strconv.ParseInt(vars["id"], 0, 64)
	uid := ctx.Get(r, "user_id").(int64)

	// Parse the request body to get the list of target rids
	var req struct {
		RIds []string `json:"rids"`
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		JSONResponse(w, models.Response{Success: false, Message: "Error reading request body"}, http.StatusBadRequest)
		return
	}
	if len(bodyBytes) == 0 || len(bytes.TrimSpace(bodyBytes)) == 0 {
		// Empty body means "send all"
		req.RIds = nil
	} else if err := json.Unmarshal(bodyBytes, &req); err != nil {
		JSONResponse(w, models.Response{Success: false, Message: "Invalid JSON structure"}, http.StatusBadRequest)
		return
	}

	// Get the campaign to validate ownership
	campaign, err := models.GetCampaign(id, uid)
	if err != nil {
		log.Error(err)
		JSONResponse(w, models.Response{Success: false, Message: "Campaign not found"}, http.StatusNotFound)
		return
	}

	// Only allow manual sending for campaigns with ManualSend enabled
	if !campaign.ManualSend {
		JSONResponse(w, models.Response{Success: false, Message: "Manual send is not enabled for this campaign"}, http.StatusBadRequest)
		return
	}

	// Trigger sending via the worker
	err = as.worker.SendSelectedTargets(campaign, req.RIds)
	if err != nil {
		log.Error(err)
		JSONResponse(w, models.Response{Success: false, Message: "Error sending emails: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	JSONResponse(w, models.Response{Success: true, Message: "Emails queued for sending"}, http.StatusOK)
}

// safeFilename strips characters that could enable HTTP header injection
// from a filename, specifically carriage return, line feed, and double quotes.
func safeFilename(name string) string {
	name = strings.NewReplacer("\r", "", "\n", "", "\"", "'").Replace(name)
	return name
}

// normalizeExportedURL makes sure a URL with an empty path is exported as
// "http://host:port/?rid=..." instead of "http://host:port?rid=...". The two
// forms are equivalent over HTTP (the request path is "/" either way), but the
// explicit slash matches the shape of the tracking URL in the next column.
func normalizeExportedURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "" {
		return raw
	}
	u.Path = "/"
	return u.String()
}

// escapeCSVFormula prevents CSV formula injection by prepending a single quote
// to any cell value that starts with a formula trigger character (=, +, -, @).
func escapeCSVFormula(val string) string {
	if len(val) == 0 {
		return val
	}
	switch val[0] {
	case '=', '+', '-', '@':
		return "'" + val
	}
	return val
}
