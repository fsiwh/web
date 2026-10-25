package audit_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/testdb"
)

func TestAuditPersistsCorrelationAfterRequestCancellation(t *testing.T) {
	db, err := database.NewConnection(testdb.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx = audit.WithRecorder(ctx, audit.NewStore(db))
	ctx, requestID := audit.WithRequest(ctx, "POST", "POST /admin/submissions/{submission}/review")
	ctx = audit.WithActor(ctx, 900)
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	audit.Audit(ctx, "submission.reviewed", "success", 42,
		slog.String("submission_id", "public-submission"),
		slog.Group("change", slog.String("status", "approved")))
	var occurredAt, event, outcome, correlation, method, route, submission, status string
	var actor, subject int64
	err = db.QueryRow(`SELECT occurred_at,event,outcome,actor_user_id,user_id,request_id,method,route,
		json_extract(details,'$.submission_id'),json_extract(details,'$.change.status') FROM audit_events`).Scan(
		&occurredAt, &event, &outcome, &actor, &subject, &correlation, &method, &route, &submission, &status)
	if err != nil {
		t.Fatal(err)
	}
	if actor != 900 || subject != 42 || correlation != requestID || event != "submission.reviewed" || outcome != "success" || method != "POST" || route != "POST /admin/submissions/{submission}/review" || submission != "public-submission" || status != "approved" {
		t.Fatal("persisted audit lost correlation, actor, subject or details")
	}
	if _, err := time.Parse(time.RFC3339Nano, occurredAt); err != nil || !strings.HasSuffix(occurredAt, "Z") {
		t.Fatal("audit timestamp is not UTC RFC3339", err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password) VALUES(42,'person@example.org','Person','unused'); DELETE FROM users WHERE id=42`); err != nil {
		t.Fatal("audit history interfered with account deletion", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE user_id=42`).Scan(&count); err != nil || count != 1 {
		t.Fatal("audit history did not survive deletion", err)
	}
}

func TestAuditStorageFailureRetainsLogOutput(t *testing.T) {
	db, err := database.NewConnection(testdb.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	var logs bytes.Buffer
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx = audit.WithRecorder(ctx, audit.NewStore(db))
	audit.Audit(ctx, "account.login", "success", 42)
	if !strings.Contains(logs.String(), `"event":"account.login"`) || !strings.Contains(logs.String(), "audit persistence failed") {
		t.Fatal("storage failure lost the event or was not reported")
	}
}

func TestEditorialLabelsCannotIntroduceLogLines(t *testing.T) {
	db, err := database.NewConnection(testdb.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var logs bytes.Buffer
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&logs, nil)))
	ctx = audit.WithRecorder(ctx, audit.NewStore(db))
	audit.Content(ctx, "content.news_saved", "News\r\nforged-entry", "/news")
	var label string
	if err := db.QueryRow(`SELECT json_extract(details,'$.content_label') FROM audit_events`).Scan(&label); err != nil {
		t.Fatal(err)
	}
	if label != "Newsforged-entry" || strings.Count(logs.String(), "\n") != 1 || strings.Contains(logs.String(), "\r") {
		t.Fatal("content label retained log delimiters")
	}
}
