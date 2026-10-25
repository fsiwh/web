package site

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/model/organization"
)

func TestMasterDataFollowsAdminFormAndPublicationPatterns(t *testing.T) {
	c, db := testApplication(t)
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	if status, _, headers := b.get("/admin/settings"); status != 303 || headers.Get("Location") != "/login" {
		t.Fatal("master data is public")
	}
	b.login()
	for _, path := range []string{"/admin/settings", "/admin/settings/identity", "/admin/settings/contact", "/admin/settings/legal", "/admin/settings/links"} {
		if status, body, _ := b.get(path); status != 200 || !strings.Contains(body, "Stammdaten") {
			t.Fatalf("missing form: %s %d", path, status)
		}
	}
	form := url.Values{"Name": {"Changed Council"}, "ShortName": {"Example Council"}, "FormalName": {"Example Representative Body"}, "Institution": {"Example University"}}
	if status, _, _ := b.post("/admin/settings/identity", form); status != 403 {
		t.Fatal("missing CSRF accepted")
	}
	form.Set("csrf", b.csrf)
	form.Set("Name", "")
	if status, body, _ := b.post("/admin/settings/identity", form); status != 422 || !strings.Contains(body, `href="#Name"`) || !strings.Contains(body, `value="Example University"`) {
		t.Fatal("invalid form lost input or field errors")
	}
	if c.organization.Current().Name == "" {
		t.Fatal("invalid submission changed published settings")
	}
	form.Set("Name", "Changed Council")
	if status, _, headers := b.post("/admin/settings/identity", form); status != 303 || headers.Get("Location") != "/admin/settings" {
		t.Fatal("valid changes did not redirect")
	}
	if status, body, _ := b.get("/"); status != 200 || !strings.Contains(body, "Changed Council") || !strings.Contains(body, "Example Council") {
		t.Fatal("saved identity not immediately visible")
	}
	loaded, err := organization.NewStore(context.Background(), db)
	if err != nil || loaded.Current().Name != "Changed Council" {
		t.Fatal("settings were not durable", err)
	}
	form = url.Values{"csrf": {b.csrf}, "SocialLinksTitle": {"Bad link"}, "SocialLinksURL": {"javascript:alert(1)"}}
	if status, body, _ := b.post("/admin/settings/links", form); status != 422 || !strings.Contains(body, `href="#SocialLinks"`) {
		t.Fatal("unsafe URL accepted or error not linked")
	}
	form.Set("SocialLinksURL", "https://social.example.org/")
	if status, _, _ := b.post("/admin/settings/links", form); status != 303 {
		t.Fatal("valid links not saved")
	}
	if _, body, _ := b.get("/contact"); !strings.Contains(body, `href="https://social.example.org/"`) || strings.Contains(body, "discord.com/invite") {
		t.Fatal("public links did not follow saved master data")
	}
	var raw string
	if err := db.QueryRow("SELECT data FROM organization WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var details organization.Details
	if err := json.Unmarshal([]byte(raw), &details); err != nil || details.ContactEmail == "" {
		t.Fatal("unrelated contact data lost", err)
	}
	if _, err := db.Exec("UPDATE users SET role='editor' WHERE id=900"); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := b.get("/admin/settings"); status != 200 {
		t.Fatal("editor cannot access master data")
	}
	details.Name = "Protected identity"
	if _, err := c.organization.Save(context.Background(), 900, details); err != organization.ErrForbidden {
		t.Fatal("model accepted non-administrator", err)
	}
}

func TestMasterDataRevisionDate(t *testing.T) {
	c, db := testApplication(t)
	if _, err := db.Exec(`UPDATE organization SET data=json_set(data,'$.LegalUpdated','2000-01-01') WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	var err error
	c.organization, err = organization.NewStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	d := c.organization.Current()
	values := url.Values{
		"csrf": {b.csrf}, "HostingName": {d.HostingName}, "HostingURL": {d.HostingURL},
		"PrivacyURL": {d.PrivacyURL}, "SupervisionName": {d.SupervisionName}, "SupervisionURL": {d.SupervisionURL},
		"PrivacyAuthorityName": {d.PrivacyAuthorityName}, "PrivacyAuthorityURL": {d.PrivacyAuthorityURL},
	}
	for _, link := range d.RegulationLinks {
		values.Add("RegulationLinksTitle", link.Title)
		values.Add("RegulationLinksURL", link.URL)
	}
	if status, body, _ := b.get("/admin/settings/legal"); status != 200 || strings.Contains(body, "Stand der rechtlichen Informationen") || strings.Contains(body, "LegalUpdated-day") {
		t.Fatal("revision date should not be editable")
	}
	if _, body, _ := b.get("/admin/settings/legal"); !strings.Contains(body, `name="RegulationLinksURL"`) || strings.Contains(body, `name="RegulationLinksTitle"`) || strings.Contains(body, "Zum Entfernen") || strings.Contains(body, "Neue Verknüpfung") {
		t.Fatal("legal links are missing or contain unnecessary guidance")
	}
	if _, body, _ := b.get("/admin/settings/links"); strings.Contains(body, `name="RegulationLinksTitle"`) || strings.Contains(body, "Zum Entfernen") {
		t.Fatal("legal links still appear in the general links form")
	}
	values.Set("LegalUpdated", "1900-01-01")
	if status, _, _ := b.post("/admin/settings/legal", values); status != 303 || c.organization.Current().LegalUpdated != d.LegalUpdated {
		t.Fatal("unchanged save or submitted date changed revision date")
	}
	if got := c.organization.Current(); len(got.RegulationLinks) != len(d.RegulationLinks) || len(got.SocialLinks) != len(d.SocialLinks) {
		t.Fatal("legal form lost legal or social links")
	}
	values.Del("RegulationLinksTitle")
	values["RegulationLinksURL"][0] = "https://example.org/statute.pdf"
	if status, _, _ := b.post("/admin/settings/legal", values); status != 303 {
		t.Fatal("URL-only legal links form could not be saved")
	}
	if got := c.organization.Current().RegulationLinks[0]; got.Title != d.RegulationLinks[0].Title || got.URL != "https://example.org/statute.pdf" {
		t.Fatal("URL-only edit did not preserve the document heading", got)
	}
	if c.organization.Current().LegalUpdated != time.Now().UTC().Format(time.DateOnly) {
		t.Fatal("legal change did not update revision date automatically")
	}
	values.Set("HostingURL", "invalid")
	if status, _, _ := b.post("/admin/settings/legal", values); status != 422 {
		t.Fatal("invalid legal information was accepted")
	}
}

func TestMasterDataTaskListReflectsSavedEntries(t *testing.T) {
	c, _ := testApplication(t)
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	original := c.organization.Current()
	d := original
	d.Office, d.ChairRole, d.PublicityRole = "", "", ""
	d.SocialLinks, d.ElectionLinks, d.RegulationLinks = nil, nil, nil
	if fields, err := c.organization.Save(context.Background(), 900, d); err != nil || len(fields) != 0 {
		t.Fatal("cannot save optional missing entries", fields, err)
	}
	check := func(incomplete bool) {
		t.Helper()
		status, body, _ := b.get("/admin/settings")
		if status != 200 || !strings.Contains(body, `class="govuk-task-list"`) {
			t.Fatal("missing task list")
		}
		for _, id := range []string{"identity"} {
			if !htmlContains(body, `id="stammdaten-`+id+`-status">Vollständig`) {
				t.Fatalf("%s should be complete", id)
			}
		}
		for _, id := range []string{"contact", "legal", "links"} {
			marker := `id="stammdaten-` + id + `-status">`
			if incomplete {
				marker += `<strong class="govuk-tag govuk-tag--blue">Unvollständig`
			} else {
				marker += "Vollständig"
			}
			if !htmlContains(body, marker) {
				t.Fatalf("incorrect saved completeness for %s", id)
			}
		}
		if incomplete && (!htmlContains(body, "Fehlend oder ungültig: Funktion für den Vorsitz") || !htmlContains(body, "Soziale Kanäle, Wahlen und Selbstverwaltung") || !htmlContains(body, "Fehlend oder ungültig: Satzungen und Ordnungen")) {
			t.Fatal("missing entries are not identified")
		}
	}
	check(true)
	b.get("/admin/settings/contact")
	check(true)
	if fields, err := c.organization.Save(context.Background(), 900, original); err != nil || len(fields) != 0 {
		t.Fatal(fields, err)
	}
	check(false)
}

func TestLegalRevisionFollowsLinkedContactChanges(t *testing.T) {
	c, db := testApplication(t)
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	reset := func() {
		t.Helper()
		if _, err := db.Exec(`UPDATE organization SET data=json_set(data,'$.LegalUpdated','2000-01-01') WHERE id=1`); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		date, err := time.Parse(time.DateOnly, want)
		if err != nil {
			t.Fatal(err)
		}
		if status, body, _ := b.get("/legal"); status != 200 || !strings.Contains(body, "Zuletzt aktualisiert am "+date.Format("02.01.2006")) {
			t.Fatal("legal revision was not published immediately")
		}
	}
	reset()
	chair := c.members.Members()[0]
	if errors := c.members.SaveMember(chair); len(errors) != 0 {
		t.Fatal(errors)
	}
	role := c.members.Roles("board")[0]
	if errors := c.members.SaveRole(role); len(errors) != 0 {
		t.Fatal(errors)
	}
	check("2000-01-01")
	chair.Name = "Updated Chair"
	if errors := c.members.SaveMember(chair); len(errors) != 0 {
		t.Fatal(errors)
	}
	today := time.Now().UTC().Format(time.DateOnly)
	check(today)
	// An unchanged Stammdaten save must preserve the date written by contact changes.
	if fields, err := c.organization.Save(context.Background(), 900, c.organization.Current()); err != nil || len(fields) != 0 {
		t.Fatal(fields, err)
	}
	check(today)
	for _, statement := range []string{
		`UPDATE responsibilities SET email='new-chair@example.org' WHERE id='1'`,
		`DELETE FROM responsibility_members WHERE responsibility_id='1'`,
		`INSERT INTO responsibility_members(responsibility_id,person_id,position) VALUES('1','1',0)`,
	} {
		reset()
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		check(today)
	}
	if _, err := db.Exec(`UPDATE people SET user_id=900 WHERE id='1'`); err != nil {
		t.Fatal(err)
	}
	reset()
	if _, err := db.Exec(`UPDATE users SET name='Updated linked account' WHERE id=900`); err != nil {
		t.Fatal(err)
	}
	check(today)
}

func TestContactWithoutOffice(t *testing.T) {
	c, _ := testApplication(t)
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	d := c.organization.Current()
	d.Office = ""
	if fields, err := c.organization.Save(context.Background(), 900, d); err != nil || len(fields) != 0 {
		t.Fatal(fields, err)
	}
	if status, body, _ := b.get("/contact"); status != 200 || strings.Contains(body, "in Raum") || !strings.Contains(body, d.Street) {
		t.Fatal("missing office left incomplete room text or removed the address")
	}
}
