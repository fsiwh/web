package site

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestForgotPassword(t *testing.T) {
	handler, err := testHandler(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, email string
		status              int
		contains            string
	}{
		{"form", "GET", "", 200, "Link anfordern"},
		{"empty", "POST", "", 422, "Gib deine E-Mail-Adresse ein."},
		{"invalid", "POST", "not-an-email", 422, "Gib eine vollständige E-Mail-Adresse ein"},
		{"other domain", "POST", "person@example.org", 422, "Verwende eine E-Mail-Adresse mit"},
		{"lookalike", "POST", "person@council.example.invalid.example.org", 422, "Verwende eine E-Mail-Adresse mit"},
		{"student", "POST", "person@students.example.invalid", 303, ""},
		{"staff", "POST", "person@council.example.invalid", 303, ""},
		{"trim and case", "POST", "  Person@COUNCIL.EXAMPLE.INVALID  ", 303, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"email": {tc.email}}
			req := httptest.NewRequest(tc.method, "/password/forgot", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			handler.ForgotPassword(response, req)
			body := response.Body.String()
			if response.Code != tc.status || !strings.Contains(body, tc.contains) {
				t.Fatalf("unexpected response: %d %s", response.Code, body)
			}
			if response.Header().Get("Cache-Control") != "no-store" || len(response.Result().Cookies()) > 0 {
				t.Fatal("recovery response cached or created a session")
			}
			if tc.status == 422 && (!strings.Contains(body, "Fehler: Passwort vergessen") || !strings.Contains(body, `href="#email"`)) {
				t.Fatal("missing accessible validation response")
			}
			if tc.status == 503 && (strings.Contains(body, tc.email) || strings.Contains(body, "Link anfordern")) {
				t.Fatal("unavailable response retained form or email")
			}
		})
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/password/forgot", strings.NewReader(strings.Repeat("x", 65<<10)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ForgotPassword(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized form status = %d", response.Code)
	}
}
