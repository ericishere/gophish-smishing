package controllers

import (
	"html/template"
	"image/png"
	"net/http"
	"strconv"

	"github.com/gophish/gophish/auth"
	ctx "github.com/gophish/gophish/context"
	"github.com/gophish/gophish/controllers/api"
	log "github.com/gophish/gophish/logger"
	"github.com/gophish/gophish/models"
	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
)

func clearTwoFactorSession(session *sessions.Session) {
	for _, key := range []string{"two_factor_challenge", "two_factor_enrollment"} {
		if token, ok := session.Values[key].(string); ok {
			models.CancelTwoFactor(token)
		}
		delete(session.Values, key)
	}
	delete(session.Values, "id")
	delete(session.Values, "two_factor_version")
	delete(session.Values, "two_factor_next")
}

func (as *AdminServer) saveSession(w http.ResponseWriter, r *http.Request) bool {
	session := ctx.Get(r, "session").(*sessions.Session)
	session.Options.Secure = as.config.UseTLS
	session.Options.HttpOnly = true
	session.Options.SameSite = http.SameSiteLaxMode
	if err := session.Save(r, w); err != nil {
		log.Error(err)
		http.Error(w, "Unable to save session", http.StatusInternalServerError)
		return false
	}
	return true
}

func (as *AdminServer) saveAuthenticatedSession(w http.ResponseWriter, r *http.Request, u models.User) bool {
	session := ctx.Get(r, "session").(*sessions.Session)
	clearTwoFactorSession(session)
	session.Values["id"] = u.Id
	session.Values["two_factor_version"] = u.TwoFactorVersion
	return as.saveSession(w, r)
}

func (as *AdminServer) startLogin(w http.ResponseWriter, r *http.Request, u models.User) {
	if u.AccountLocked {
		as.handleInvalidLogin(w, r, "Account Locked")
		return
	}
	session := ctx.Get(r, "session").(*sessions.Session)
	clearTwoFactorSession(session)
	purpose := ""
	if len(u.TwoFactorMethods) > 0 {
		purpose = models.TwoFactorLogin
	} else if u.TwoFactorRequired {
		purpose = models.TwoFactorSetup
	}
	if purpose != "" {
		token, _, err := models.BeginTwoFactor(u, purpose)
		if err != nil {
			as.handleInvalidLogin(w, r, err.Error())
			return
		}
		session.Values["two_factor_challenge"] = token
		session.Values["two_factor_next"] = r.FormValue("next")
		if !as.saveSession(w, r) {
			return
		}
		http.Redirect(w, r, "/two_factor", http.StatusFound)
		return
	}
	if err := models.RecordLogin(u.Id); err != nil {
		http.Error(w, "Unable to complete sign in", http.StatusInternalServerError)
		return
	}
	if !as.saveAuthenticatedSession(w, r, u) {
		return
	}
	as.nextOrIndex(w, r)
}

func twoFactorToken(r *http.Request, settings bool) string {
	key := "two_factor_challenge"
	if settings {
		key = "two_factor_enrollment"
	}
	token, _ := ctx.Get(r, "session").(*sessions.Session).Values[key].(string)
	return token
}

// challengeForRequest keeps settings enrollment separate from login proof.
func challengeForRequest(r *http.Request, settings bool) (models.TwoFactorChallenge, models.User, error) {
	challenge, u, err := models.GetTwoFactorChallenge(twoFactorToken(r, settings))
	if err != nil {
		return challenge, u, err
	}
	if settings {
		current, ok := ctx.Get(r, "user").(models.User)
		if !ok || current.Id != u.Id || challenge.Purpose != models.TwoFactorEnroll {
			return challenge, u, models.ErrTwoFactorExpired
		}
	} else if challenge.Purpose != models.TwoFactorLogin && challenge.Purpose != models.TwoFactorSetup {
		return challenge, u, models.ErrTwoFactorExpired
	}
	return challenge, u, nil
}

func noStoreTwoFactor(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	// Keep same-origin form metadata for CSRF validation without sending
	// referrers to external sites. no-referrer makes browsers send Origin: null.
	w.Header().Set("Referrer-Policy", "same-origin")
}

func (as *AdminServer) renderTwoFactor(w http.ResponseWriter, r *http.Request, challenge models.TwoFactorChallenge, u models.User, settings bool, message string, status int) {
	noStoreTwoFactor(w)
	title, qrPath, cancelPath := "Two-Factor Authentication", "/two_factor/qr", "/logout"
	if settings {
		qrPath, cancelPath = "/settings/two_factor/qr", "/settings#twoFactorSettings"
	}
	setup := challenge.Purpose != models.TwoFactorLogin
	if setup {
		title = "Set Up Two-Factor Authentication"
	}
	params := struct {
		Title, Username, Token, Secret, QRPath, CancelPath, Error, Name string
		Setup, Required                                                 bool
	}{
		Title: title, Username: u.Username, Token: csrf.Token(r),
		Secret: challenge.Secret, QRPath: qrPath, CancelPath: cancelPath,
		Error: message, Name: r.FormValue("name"), Setup: setup,
		Required: challenge.Purpose == models.TwoFactorSetup,
	}
	templates, err := template.ParseFiles("templates/two_factor.html")
	if err != nil {
		http.Error(w, "Unable to render verification", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err = templates.ExecuteTemplate(w, "base", params); err != nil {
		log.Error(err)
	}
}

// TwoFactor handles the restricted sign-in stage. No user ID or API key is
// issued to the browser until code verification/enrollment succeeds.
func (as *AdminServer) TwoFactor(w http.ResponseWriter, r *http.Request) {
	noStoreTwoFactor(w)
	challenge, u, err := challengeForRequest(r, false)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if r.Method == http.MethodPost {
		u, err = models.CompleteTwoFactor(twoFactorToken(r, false), r.FormValue("code"), r.FormValue("name"))
		if err != nil {
			status := http.StatusBadRequest
			if err == models.ErrTwoFactorRateLimit {
				status = http.StatusTooManyRequests
			}
			as.renderTwoFactor(w, r, challenge, u, false, err.Error(), status)
			return
		}
		session := ctx.Get(r, "session").(*sessions.Session)
		next, _ := session.Values["two_factor_next"].(string)
		if err = models.RecordLogin(u.Id); err != nil {
			http.Error(w, "Unable to complete sign in", http.StatusInternalServerError)
			return
		}
		if !as.saveAuthenticatedSession(w, r, u) {
			return
		}
		r.Form.Set("next", next)
		as.nextOrIndex(w, r)
		return
	}
	as.renderTwoFactor(w, r, challenge, u, false, "", http.StatusOK)
}

// TwoFactorQR produces the provisioning image locally; secrets are never sent
// to an external QR service or included in a URL.
func (as *AdminServer) TwoFactorQR(w http.ResponseWriter, r *http.Request) {
	noStoreTwoFactor(w)
	settings := r.URL.Path == "/settings/two_factor/qr"
	challenge, u, err := challengeForRequest(r, settings)
	if err != nil || challenge.Secret == "" {
		http.NotFound(w, r)
		return
	}
	key, err := auth.TOTPKey(u.Username, challenge.Secret)
	if err != nil {
		http.Error(w, "Unable to create QR code", http.StatusInternalServerError)
		return
	}
	img, err := key.Image(240, 240)
	if err != nil {
		http.Error(w, "Unable to create QR code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	if err = png.Encode(w, img); err != nil {
		log.Error(err)
	}
}

// TwoFactorSettings uses session authentication and CSRF protection, including
// for view-only users. Adding/deleting methods requires the current password.
func (as *AdminServer) TwoFactorSettings(w http.ResponseWriter, r *http.Request) {
	noStoreTwoFactor(w)
	u := ctx.Get(r, "user").(models.User)
	session := ctx.Get(r, "session").(*sessions.Session)
	fail := func(err error) {
		Flash(w, r, "danger", err.Error())
		if as.saveSession(w, r) {
			http.Redirect(w, r, "/settings#twoFactorSettings", http.StatusSeeOther)
		}
	}
	if r.Method == http.MethodPost && r.FormValue("action") != "confirm" {
		if err := auth.ValidatePassword(r.FormValue("current_password"), u.Hash); err != nil {
			fail(auth.ErrInvalidPassword)
			return
		}
		switch r.FormValue("action") {
		case "add":
			token, _, err := models.BeginTwoFactor(u, models.TwoFactorEnroll)
			if err != nil {
				fail(err)
				return
			}
			session.Values["two_factor_enrollment"] = token
			if as.saveSession(w, r) {
				http.Redirect(w, r, "/settings/two_factor", http.StatusSeeOther)
			}
		case "delete":
			id, err := strconv.ParseInt(r.FormValue("method_id"), 10, 64)
			if err != nil {
				http.Error(w, "Invalid method", http.StatusBadRequest)
				return
			}
			u, err = models.DeleteTwoFactorMethod(u, id)
			if err != nil {
				fail(err)
				return
			}
			if !as.saveAuthenticatedSession(w, r, u) {
				return
			}
			http.Redirect(w, r, "/settings#twoFactorSettings", http.StatusSeeOther)
		default:
			http.Error(w, "Invalid action", http.StatusBadRequest)
		}
		return
	}
	challenge, u, err := challengeForRequest(r, true)
	if err != nil {
		http.Redirect(w, r, "/settings#twoFactorSettings", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost {
		u, err = models.CompleteTwoFactor(twoFactorToken(r, true), r.FormValue("code"), r.FormValue("name"))
		if err != nil {
			status := http.StatusBadRequest
			if err == models.ErrTwoFactorRateLimit {
				status = http.StatusTooManyRequests
			}
			as.renderTwoFactor(w, r, challenge, u, true, err.Error(), status)
			return
		}
		if !as.saveAuthenticatedSession(w, r, u) {
			return
		}
		http.Redirect(w, r, "/settings#twoFactorSettings", http.StatusSeeOther)
		return
	}
	as.renderTwoFactor(w, r, challenge, u, true, "", http.StatusOK)
}

// ResetUserTwoFactor is an administrator-only, CSRF-protected recovery action.
func (as *AdminServer) ResetUserTwoFactor(w http.ResponseWriter, r *http.Request) {
	current := ctx.Get(r, "user").(models.User)
	if auth.ValidatePassword(r.FormValue("current_password"), current.Hash) != nil {
		api.JSONResponse(w, models.Response{Success: false, Message: "Invalid password"}, http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid user", http.StatusBadRequest)
		return
	}
	if _, err = models.GetUser(id); err != nil {
		http.NotFound(w, r)
		return
	}
	if err = models.ResetTwoFactor(id); err != nil {
		http.Error(w, "Unable to reset 2FA", http.StatusInternalServerError)
		return
	}
	log.Infof("User ID %d reset two-factor authentication for user ID %d", current.Id, id)
	api.JSONResponse(w, models.Response{Success: true, Message: "2FA methods reset. The user's sessions have been signed out. Required users must set up 2FA at their next sign in."}, http.StatusOK)
}
