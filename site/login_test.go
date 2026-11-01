package site

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLoginPreview(t *testing.T) {
	handler, err := testHandler(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, email, password string
		status                int
		contains              string
	}{
		{"empty", "", "", 422, "Gib deine E-Mail-Adresse ein."},
		{"invalid email", "not-an-email", "private-test-password", 422, "Gib eine vollständige E-Mail-Adresse ein"},
		{"display name is not email", "Name <name@students.example.invalid>", "private-test-password", 422, "Gib eine vollständige E-Mail-Adresse ein"},
		{"other domain", "name@example.org", "private-test-password", 422, "Verwende eine E-Mail-Adresse mit"},
		{"missing password", "name@students.example.invalid", "", 422, "Gib dein Passwort ein."},
		{"unknown user", "name@students.example.invalid", "private-test-password", 422, "E-Mail-Adresse oder Passwort ist falsch."},
		{"staff domain", "name@council.example.invalid", "private-test-password", 422, "E-Mail-Adresse oder Passwort ist falsch."},
		{"trim and domain case", "  Name@STUDENTS.EXAMPLE.INVALID  ", "private-test-password", 422, "E-Mail-Adresse oder Passwort ist falsch."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"email": {tc.email}, "password": {tc.password}}
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			handler.Login(response, req)
			body := response.Body.String()
			if response.Code != tc.status || !strings.Contains(body, tc.contains) {
				t.Fatalf("unexpected response: %d %s", response.Code, body)
			}
			if strings.Contains(body, "private-test-password") || len(response.Result().Cookies()) > 0 {
				t.Fatal("preview reflected password or created a session")
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("login response must not be cached")
			}
			if tc.status == 422 && (!strings.Contains(body, "Fehler: Anmelden") || !strings.Contains(body, "govuk-error-summary")) {
				t.Fatal("validation response missing accessible error presentation")
			}
		})
	}

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(strings.Repeat("x", 65<<10)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Login(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized form status = %d", response.Code)
	}
}
