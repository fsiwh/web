package site

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/model/users"
)

func TestUserInvitationRegistrationJourney(t *testing.T) {
	for _, role := range []string{"editor", "admin"} {
		t.Run(role, func(t *testing.T) {
			b, db := identityBrowser(t)
			if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(42,'admin@council.example.invalid','Admin','unused','admin',1,1)`); err != nil {
				t.Fatal(err)
			}
			identity := users.NewService(db, "https://example.org", users.DomainPolicy{Accounts: []string{"council.example.invalid", "students.example.invalid"}})
			session, err := identity.CreateSession(context.Background(), 42)
			if err != nil {
				t.Fatal(err)
			}
			b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
			status, body, _ := b.get("/admin/users/create")
			if status != 200 || !strings.Contains(body, "Systemadministration") || strings.Contains(body, `id="functions"`) || strings.Contains(body, `id="organization"`) {
				t.Fatal("access question missing or unrelated questions visible")
			}
			form := url.Values{"csrf": {csrfFrom(t, body)}, "step": {"access"}, "role": {role}}
			status, body, _ = b.post("/admin/users/create", form)
			if status != 200 {
				t.Fatal("access step failed", status)
			}
			if role == "editor" {
				if !strings.Contains(body, `id="kind"`) || strings.Contains(body, `name="organization"`) {
					t.Fatal("kind step missing")
				}
				form.Set("step", "kind")
				form.Set("kind", "board")
				if status, body, _ = b.post("/admin/users/create", form); status != 200 || !strings.Contains(body, "Vorsitz") || strings.Contains(body, "Öffentlichkeit") {
					t.Fatal("functions not filtered by kind")
				}
				form.Set("step", "functions")
				if status, _, _ = b.post("/admin/users/create", form); status != 422 {
					t.Fatal("empty functions accepted")
				}
				finance := publicRef(t, db, "roles", "1")
				publicity := publicRef(t, db, "roles", "6")
				form["functions"] = []string{publicity}
				if status, _, _ = b.post("/admin/users/create", form); status != 422 {
					t.Fatal("function from another kind accepted")
				}
				form["functions"] = []string{finance}
				if status, body, _ = b.post("/admin/users/create", form); status != 200 || !strings.Contains(body, "Weitere Funktionen hinzufügen") {
					t.Fatal("assignment list missing")
				}
				form.Set("step", "assignments")
				form["assigned"] = []string{finance}
				form.Del("functions")
				form.Set("add", "true")
				if status, body, _ = b.post("/admin/users/create", form); status != 200 || !strings.Contains(body, `id="kind"`) {
					t.Fatal("add another did not return to kind")
				}
				form.Del("add")
				form.Set("step", "kind")
				form.Set("kind", "departments")
				if status, _, _ = b.post("/admin/users/create", form); status != 200 {
					t.Fatal("second kind step failed")
				}
				form.Set("step", "functions")
				form["functions"] = []string{publicity}
				if status, body, _ = b.post("/admin/users/create", form); status != 200 || !strings.Contains(body, "Öffentlichkeit") || !strings.Contains(body, "Vorsitz") {
					t.Fatal("multiple assignments lost")
				}
				form.Set("step", "assignments")
				form["assigned"] = []string{finance, publicity}
				form.Del("functions")
				form.Set("remove", publicity)
				if status, body, _ = b.post("/admin/users/create", form); status != 200 || strings.Contains(body, " / Öffentlichkeit") {
					t.Fatal("remove failed")
				}
				form.Del("remove")
				form["assigned"] = []string{finance}
				if status, body, _ = b.post("/admin/users/create", form); status != 200 || !strings.Contains(body, `name="email"`) {
					t.Fatal("details step missing")
				}
			} else if strings.Contains(body, `id="functions"`) || strings.Contains(body, `name="organization"`) {
				t.Fatal("system administration asks organizational questions")
			}
			form.Set("step", "details")
			form.Set("name", "Neue Person")
			form.Set("email", "new@students.example.invalid")
			if status, _, _ = b.post("/admin/users/create", form); status != 303 {
				t.Fatal("invitation failed", status)
			}
			var link string
			var id int64
			if err := db.QueryRow(`SELECT id FROM users WHERE email='new@students.example.invalid'`).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT link FROM mail_outbox WHERE user_id=?`, id).Scan(&link); err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(link)
			if err != nil {
				t.Fatal(err)
			}
			invitee := newAdminBrowser(t, b.server)
			status, body, _ = invitee.get(target.RequestURI())
			if status != 200 || !strings.Contains(body, `id="invite-email"`) || !strings.Contains(body, "disabled") {
				t.Fatal("fixed invitation fields missing")
			}
			accept := url.Values{"csrf": {csrfFrom(t, body)}, "token": {target.Query().Get("token")}, "password": {"secure password"}, "name": {"Attacker"}, "email": {"attacker@council.example.invalid"}, "role": {"admin"}}
			if status, _, _ := invitee.post("/invitation/accept", accept); status != 303 {
				t.Fatal("accept failed", status)
			}
			var name, email, storedRole string
			if err := db.QueryRow(`SELECT name,email,role FROM users WHERE id=?`, id).Scan(&name, &email, &storedRole); err != nil || name != "Neue Person" || email != "new@students.example.invalid" || storedRole != role {
				t.Fatal("invitation details changed", err)
			}
			_, body, _ = invitee.get("/login")
			if status, _, h := invitee.post("/login", url.Values{"csrf": {csrfFrom(t, body)}, "email": {email}, "password": {"secure password"}}); status != 303 || h.Get("Location") != "/admin" {
				t.Fatal("login failed")
			}
			want := 403
			if role == "admin" {
				want = 200
			}
			if status, _, _ := invitee.get("/admin/users"); status != want {
				t.Fatal("incorrect access", status)
			}
			if status, body, _ := b.get("/admin/users/" + publicRef(t, db, "users", fmt.Sprint(id))); status != 200 || (role == "editor" && !strings.Contains(body, "Vorsitz")) {
				t.Fatal("account assignments missing")
			}
		})
	}
}
