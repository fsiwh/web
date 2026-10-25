// Package audit records security and account lifecycle events without secrets.
package audit

import (
	"context"
	"crypto/rand"
	"log/slog"
	"strings"
	"time"
)

type loggerKey struct{}
type recorderKey struct{}
type requestKey struct{}

// Content records only the public/editorial resource, never its author's
// account fields. The full audit retains the request actor for administrators.
func Content(ctx context.Context, event, label, path string) {
	label = strings.ReplaceAll(strings.ReplaceAll(label, "\n", ""), "\r", "")
	Audit(ctx, event, "success", 0, slog.String("content_label", label), slog.String("content_url", path))
}

type requestInfo struct {
	id, method, route string
	actorUserID       int64
}

// WithRecorder enables durable audit storage for requests or background work.
func WithRecorder(ctx context.Context, value Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, value)
}

func logger(ctx context.Context) *slog.Logger {
	if value, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return value
	}
	return slog.Default()
}

// WithLogger allows callers to direct audit events to a scoped logger.
func WithLogger(ctx context.Context, value *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, value)
}

// WithActor distinguishes the authenticated caller from an event's subject.
func WithActor(ctx context.Context, userID int64) context.Context {
	info, _ := ctx.Value(requestKey{}).(requestInfo)
	info.actorUserID = userID
	ctx = context.WithValue(ctx, requestKey{}, info)
	return WithLogger(ctx, logger(ctx).With("actor_user_id", userID))
}

// WithRequest assigns a server-generated correlation ID. Route is the registered
// pattern, never a raw URL: query strings can contain recovery tokens.
func WithRequest(ctx context.Context, method, route string) (context.Context, string) {
	id := rand.Text()
	ctx = context.WithValue(ctx, requestKey{}, requestInfo{id: id, method: method, route: route})
	return WithLogger(ctx, logger(ctx).With("request_id", id, "method", method, "route", route)), id
}

// Audit records an event with its outcome, actor, and optional structured details.
func Audit(ctx context.Context, event, outcome string, userID int64, details ...slog.Attr) {
	attributes := []slog.Attr{slog.String("event", event), slog.String("outcome", outcome)}
	if userID != 0 {
		attributes = append(attributes, slog.Int64("user_id", userID))
	}
	attributes = append(attributes, details...)
	logger(ctx).LogAttrs(ctx, slog.LevelInfo, "audit", attributes...)
	if recorder, ok := ctx.Value(recorderKey{}).(Recorder); ok {
		info, _ := ctx.Value(requestKey{}).(requestInfo)
		record := Record{
			Time: time.Now().UTC(), Event: event, Outcome: outcome,
			ActorUserID: info.actorUserID, UserID: userID,
			RequestID: info.id, Method: info.method, Route: info.route, Details: details,
		}
		// A disconnected client must not discard its audit trail. Bound storage
		// separately so an unavailable audit store cannot indefinitely delay work.
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := recorder.Record(writeCtx, record); err != nil {
			logger(ctx).ErrorContext(ctx, "audit persistence failed", "event", event, "outcome", outcome, "error", err)
		}
	}
}
