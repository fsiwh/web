package site

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRegistrationPreview(t *testing.T) {
	handler, err := testHandler(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, email, password, message string
		status                         int
	}{
		{"", "", "", "Bitte gib einen Namen an.", 422},
		{"A", "name@students.example.invalid", "private-test-password", "Der Name muss zwischen 2 und 64 Zeichen lang sein.", 422},
		{"Name", "name@students.example.invalid.evil.example", "private-test-password", "Verwende eine E-Mail-Adresse mit", 422},
		{"Name", "name@sub.council.example.invalid", "private-test-password", "Verwende eine E-Mail-Adresse mit", 422},
		{"Name", "Name <name@council.example.invalid>", "private-test-password", "Gib eine vollständige E-Mail-Adresse ein", 422},
		{"Name", "name@students.example.invalid", "short", "Das Passwort muss mindestens 8 Zeichen lang sein.", 422},
		{"Name", "name@students.example.invalid", strings.Repeat("x", 73), "", 303},
		{"Name", "name@students.example.invalid", "äöüßéèñ", "Das Passwort muss mindestens 8 Zeichen lang sein.", 422},
		{"Name", "name@students.example.invalid", "äöüßéèñø", "", 303},
		{"Name", "name@students.example.invalid", " a b c d", "", 303},
		{"  Name  ", "  Name@STUDENTS.EXAMPLE.INVALID  ", "private-test-password", "", 303},
		{"Name", "name@council.example.invalid", "private-test-password", "Verwende eine E-Mail-Adresse mit @students.example.invalid.", 422},
	} {
		handler, db := testApplication(t)
		seedPublicContent(t, db)
		form := url.Values{"name": {tc.name}, "email": {tc.email}, "password": {tc.password}, "program_id": {publicRef(t, db, "study_programs", "1")}, "degree": {"bachelor"}}
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.Register(response, req)
		body := response.Body.String()
		if response.Code != tc.status || !strings.Contains(body, tc.message) {
			t.Fatalf("unexpected registration response: %d %s", response.Code, body)
		}
		if response.Header().Get("Cache-Control") != "no-store" || len(response.Result().Cookies()) > 0 || strings.Contains(body, `value="private-test-password"`) {
			t.Fatal("registration cached credentials, reflected a password or created a session")
		}
		if tc.status == 422 && (!strings.Contains(body, "Fehler: Konto erstellen") || !strings.Contains(body, "govuk-error-summary")) {
			t.Fatal("missing accessible validation errors")
		}
	}
	for _, program := range []string{"", "unknown-program", "1"} {
		handler, _ := testHandler(t)
		form := url.Values{"name": {"Testperson"}, "email": {"test@students.example.invalid"}, "password": {"private-test-password"}, "program_id": {program}}
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.Register(response, req)
		if program == "" {
			if response.Code != http.StatusSeeOther {
				t.Fatal("omitting the optional programme must pass validation")
			}
		} else if response.Code != 422 || !strings.Contains(response.Body.String(), `href="#program_id"`) || !strings.Contains(response.Body.String(), "Bitte wähle deinen Studiengang aus.") {
			t.Fatal("unknown program must produce a linked field error")
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/register", nil))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("registration page unavailable or cacheable")
		}
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader("name="+strings.Repeat("x", 65<<10)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.Register(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized registration status = %d", response.Code)
	}
}

func TestRegistrationSeparatesStudyProgramAndDegree(t *testing.T) {
	handler, db := testApplication(t)
	seedPublicContent(t, db)
	if _, err := db.Exec(`INSERT INTO programs(id,name) VALUES(4,'Informatik');
		INSERT INTO program_qualifications(legacy_program_id,study_program_id,degree) VALUES(4,1,'Master');
		INSERT INTO program_versions(program_id,name) VALUES(4,'2024')`); err != nil {
		t.Fatal(err)
	}
	publicRef(t, db, "programs", "4")
	program := publicRef(t, db, "study_programs", "1")
	other := publicRef(t, db, "study_programs", "2")
	options, err := handler.identity.Programs(context.Background())
	if err != nil || len(options) != 3 {
		t.Fatal("registration must show each study program once", options, err)
	}
	for i, tc := range []struct {
		program, degree, version, errorAnchor string
		wantProgram                           int
	}{
		{program, "bachelor", "", "", 1},
		{program, "master", "", "", 4},
		{"", "", "", "", 0},
		{program, "", "", "degree", 0},
		{"", "bachelor", "", "program_id", 0},
		{other, "master", "", "degree", 0},
		{program, "Bachelor", "", "degree", 0},
		{publicRef(t, db, "programs", "1"), "bachelor", "", "program_id", 0},
		{program, "bachelor", "2023", "", 1},
		{program, "master", "2024", "", 4},
		{program, "master", "2023", "version", 0},
		{program, "bachelor", "1900", "version", 0},
		{"", "", "2023", "program_id", 0},
	} {
		email := fmt.Sprintf("qualification%d@students.example.invalid", i)
		form := url.Values{"name": {"Testperson"}, "email": {email}, "password": {"private-test-password"}, "program_id": {tc.program}, "degree": {tc.degree}, "version": {tc.version}}
		r := httptest.NewRequest("POST", "/register", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler.Register(w, r)
		var count, programID int
		if err := db.QueryRow(`SELECT count(*),COALESCE(max(program_id),0) FROM users WHERE email=?`, email).Scan(&count, &programID); err != nil {
			t.Fatal(err)
		}
		if tc.errorAnchor == "" {
			if w.Code != 303 || count != 1 || programID != tc.wantProgram {
				t.Fatal("wrong account qualification", i, w.Code, count, programID)
			}
			if tc.version != "" {
				var userID int64
				if err := db.QueryRow(`UPDATE users SET verified=1 WHERE email=? RETURNING id`, email).Scan(&userID); err != nil {
					t.Fatal(err)
				}
				settings, err := handler.exams.Settings(context.Background(), userID)
				if err != nil || len(settings.Versions) != 1 || settings.Versions[0] != tc.version || len(settings.Degrees) != 1 || settings.Degrees[0] != tc.degree || len(settings.Programs) != 1 || settings.Programs[0] != tc.program {
					t.Fatal("registration qualification did not initialize exam preferences", settings, err)
				}
				var stored string
				if err := db.QueryRow(`SELECT json_extract(programs,'$[0]') FROM user_archive_preferences WHERE user_id=?`, userID).Scan(&stored); err != nil || stored != tc.program {
					t.Fatal("registration must store a canonical study program", stored, err)
				}
			}
		} else if w.Code != 422 || count != 0 || !strings.Contains(w.Body.String(), `href="#`+tc.errorAnchor+`"`) || !strings.Contains(w.Body.String(), `name="degree"`) {
			t.Fatal("invalid qualification accepted or inaccessible field error", i, w.Code, count)
		}
	}
	directory, err := handler.identity.Directory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := directory.Search("qualification1@", 1)
	if len(page.Users) != 1 || page.Users[0].Program != "Informatik" || page.Users[0].Degree != "Master" {
		t.Fatal("account display must keep study program and degree separate", page.Users)
	}
}
