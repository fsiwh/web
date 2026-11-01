package site

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/audit"
	"golang.org/x/crypto/bcrypt"
)

func TestAccountAuditCorrelatesRequestsWithoutLeakingSecrets(t *testing.T) {
	handler, db := testApplication(t)
	const email = "private-student@students.example.invalid"
	const password = "private student password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(1442,?,'Student',?,'user',1,1)`, email, string(hash)); err != nil {
		t.Fatal(err)
	}
	session, err := handler.identity.CreateSession(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx = audit.WithRecorder(ctx, audit.NewStore(db))
	form := url.Values{"email": {email}, "password": {password}, "csrf": {session.CSRF}}
	req := httptest.NewRequest("POST", "/login?token=private-query-token", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Request-ID", "untrusted-request-id")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session.ID})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("login failed: %d", response.Code)
	}
	requestID := response.Header().Get("X-Request-ID")
	if requestID == "" || requestID == "untrusted-request-id" {
		t.Fatal("missing server-generated request ID")
	}
	found := map[string]bool{}
	decoder := json.NewDecoder(strings.NewReader(logs.String()))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record["request_id"] != requestID || record["route"] != "POST /login" || record["user_id"] != float64(1442) {
			t.Fatalf("missing request or account correlation: %v", record)
		}
		found[record["event"].(string)] = true
	}
	for _, event := range []string{"account.login", "session.created", "http.request"} {
		if !found[event] {
			t.Errorf("missing %s audit", event)
		}
	}
	var stored string
	if err := db.QueryRow(`SELECT json_group_array(json_object('event',event,'details',details,'route',route,'request_id',request_id)) FROM audit_events`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE request_id=?`, requestID).Scan(&count); err != nil || count != 3 {
		t.Fatal("request audit was not persisted", err)
	}
	for _, secret := range []string{email, password, string(hash), session.ID, session.CSRF, "private-query-token", "untrusted-request-id"} {
		if strings.Contains(logs.String(), secret) || strings.Contains(stored, secret) {
			t.Error("audit leaked request or credential data")
		}
	}
}

func TestCSRFAuditIncludesRejectedOutcomeWithoutFormData(t *testing.T) {
	handler, _ := testApplication(t)
	var logs bytes.Buffer
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
	req := httptest.NewRequest("POST", "/register?token=secret-link", strings.NewReader("email=secret-email&password=secret-password&csrf=secret-csrf")).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("CSRF rejection status: %d", response.Code)
	}
	for _, want := range []string{`"event":"request.csrf"`, `"outcome":"rejected"`, `"status":403`, `"route":"POST /register"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("missing audit detail %s", want)
		}
	}
	if strings.Contains(logs.String(), "secret-") {
		t.Fatal("rejected request audit leaked form or query data")
	}
}

func TestAdminRecoveryAuditDistinguishesActorFromAccount(t *testing.T) {
	handler, db := testApplication(t)
	session, err := handler.identity.CreateSession(context.Background(), 900)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
	form := url.Values{"csrf": {session.CSRF}}
	target := publicRef(t, db, "users", "42")
	req := httptest.NewRequest("POST", "/admin/users/"+target+"/reset", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session.ID})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("admin recovery failed: %d", response.Code)
	}
	found := false
	decoder := json.NewDecoder(strings.NewReader(logs.String()))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record["event"] == "account.password_reset_requested" {
			found = true
			if record["actor_user_id"] != float64(900) || record["user_id"] != float64(42) {
				t.Fatalf("admin and affected account conflated: %v", record)
			}
		}
	}
	if !found {
		t.Fatal("missing admin recovery audit")
	}
}
