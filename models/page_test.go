package models

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
	"gopkg.in/check.v1"
)

func (s *ModelsSuite) TestPostPage(c *check.C) {
	html := `<html><body><form id="login" action="https://example.com" method="GET">
		<input name="username"/><input name="password" type="password"/>
		<textarea name="account"></textarea><select name="secret"><option>test</option></select>
		<button name="submit" formaction="https://example.com" formmethod="GET">Submit</button>
		</form><input form="login" name="external-account"/></body></html>`
	p := Page{Name: "Test Page", HTML: html, RedirectURL: "http://example.com",
		CaptureCredentials: true, CapturePasswords: true}
	c.Assert(PostPage(&p), check.IsNil)
	assertCaptureDisabled(c, p)
	c.Assert(p.RedirectURL, check.Equals, "http://example.com")

	// API updates cannot re-enable collection either.
	p.CaptureCredentials, p.CapturePasswords = true, true
	p.HTML = html
	p.RedirectURL = ""
	c.Assert(PutPage(&p), check.IsNil)
	assertCaptureDisabled(c, p)
	c.Assert(p.RedirectURL, check.Equals, "")

	// Simulate a page saved before collection was disabled. All read paths
	// must disable its controls without requiring an administrator to resave it.
	c.Assert(db.Model(&p).Updates(map[string]interface{}{
		"html": html, "capture_credentials": true, "capture_passwords": true,
	}).Error, check.IsNil)
	loaded, err := GetPage(p.Id, p.UserId)
	c.Assert(err, check.IsNil)
	assertCaptureDisabled(c, loaded)
	loaded, err = GetPageByName(p.Name, p.UserId)
	c.Assert(err, check.IsNil)
	assertCaptureDisabled(c, loaded)
	pages, err := GetPages(p.UserId)
	c.Assert(err, check.IsNil)
	c.Assert(len(pages), check.Equals, 1)
	assertCaptureDisabled(c, pages[0])
}

func assertCaptureDisabled(c *check.C, p Page) {
	c.Assert(p.CaptureCredentials, check.Equals, false)
	c.Assert(p.CapturePasswords, check.Equals, false)
	d, err := goquery.NewDocumentFromReader(strings.NewReader(p.HTML))
	c.Assert(err, check.IsNil)
	c.Assert(d.Find("[name], [formaction], [formmethod]").Length(), check.Equals, 0)
	action, _ := d.Find("form").Attr("action")
	method, _ := d.Find("form").Attr("method")
	c.Assert(action, check.Equals, "")
	c.Assert(method, check.Equals, "POST")
}

func (s *ModelsSuite) TestPageValidation(c *check.C) {
	html := `<html>
			<head></head>
			<body>{{.BaseURL}}</body>
		  </html>`
	p := Page{
		HTML:        html,
		RedirectURL: "http://example.com",
	}
	// Validate that a name is required
	err := p.Validate()
	c.Assert(err, check.Equals, ErrPageNameNotSpecified)

	p.Name = "Test Page"

	// Capture settings cannot be enabled through validation.
	p.CaptureCredentials, p.CapturePasswords = true, true
	err = p.Validate()
	c.Assert(err, check.Equals, nil)
	c.Assert(p.CaptureCredentials, check.Equals, false)
	c.Assert(p.CapturePasswords, check.Equals, false)

	// Validate that if the HTML contains an invalid template tag, that we
	// catch it
	p.HTML = `<html>
		<head></head>
		<body>{{.INVALIDTAG}}</body>
	  </html>`
	err = p.Validate()
	c.Assert(err, check.NotNil)

	// Validate that if the RedirectURL contains an invalid template tag, that
	// we catch it
	p.HTML = "valid data"
	p.RedirectURL = "http://example.com/{{.INVALIDTAG}}"
	err = p.Validate()
	c.Assert(err, check.NotNil)
}
