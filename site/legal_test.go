package site

import (
	"net/http/httptest"
	"strings"
	"testing"

	"context"
)

func TestLegalChairFollowsMemberAssignments(t *testing.T) {
	c, db := testApplication(t)
	store := c.members
	if _, err := db.Exec(`INSERT INTO users(id,name,email,password,role) VALUES(999,'Neue Vorsitzperson','private@example.test','!','editor')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, role := range store.Roles("board") {
		if role.Title == "Vorsitz" {
			role.Title = "Sprecherkreis"
			role.MemberIDs = []string{"999"}
			if errors := store.SaveRole(role); len(errors) != 0 {
				t.Fatal(errors)
			}
		}
	}
	w := httptest.NewRecorder()
	c.Legal(w, httptest.NewRequest("GET", "/legal", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Neue Vorsitzperson") || strings.Contains(body, "private@example.test") {
		t.Fatalf("imprint did not use public current chair data: %d %s", w.Code, body)
	}
}

func TestDocumentErrorsKeepStatusAndRecoveryWithoutDetails(t *testing.T) {
	handler, err := testHandler(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/missing", "/events/999", "/media/albums/missing"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		body := w.Body.String()
		if w.Code != 404 || !strings.Contains(body, `<html lang="de"`) || !strings.Contains(body, `<h1 class="govuk-heading-xl">Seite nicht gefunden</h1>`) || !strings.Contains(body, `href="/sitemap"`) {
			t.Fatalf("missing document recovery for %s: %d %s", path, w.Code, body)
		}
	}
}
