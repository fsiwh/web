package site

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/model/users"
)

const sessionCookie = "web-session"

type session = users.Session

// Policies are attached to concrete registrations, before session processing.
// There is deliberately no pathname-based security classification.
type routePolicy struct {
	session, authenticated, admin, reviewer, csrf, private bool
	passive                                                bool
	bodyLimit                                              int64
}

func randomToken() string {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

type sessionResponse struct {
	http.ResponseWriter
	session      *session
	canonicalURL string
	status       int
}

func (w *sessionResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 200 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *sessionResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *sessionResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Transient UI state is process-local and is never written to account storage.
var transient = struct {
	sync.Mutex
	flash map[string]string
}{flash: make(map[string]string)}

func flash(w http.ResponseWriter, message string) {
	if response, ok := w.(*sessionResponse); ok && response.session != nil {
		transient.Lock()
		defer transient.Unlock()
		transient.flash[response.session.ID] = message
	}
}

func takeFlash(w http.ResponseWriter) string {
	if response, ok := w.(*sessionResponse); ok && response.session != nil {
		transient.Lock()
		defer transient.Unlock()
		message := transient.flash[response.session.ID]
		delete(transient.flash, response.session.ID)
		return message
	}
	return ""
}

func (c *Handler) setSessionCookie(w http.ResponseWriter, r *http.Request, s *session) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.ID, Path: "/", HttpOnly: true, Secure: c.secureCookies || r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: max(1, int(time.Until(s.Expires).Seconds())), Expires: s.Expires})
}

func (c *Handler) withSession(policy routePolicy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, requestID := audit.WithRequest(r.Context(), r.Method, r.Pattern)
		r = r.WithContext(ctx)
		response := &sessionResponse{ResponseWriter: w}
		w = response
		w.Header().Set("X-Request-ID", requestID)
		defer func() {
			if r.Method != http.MethodPost && response.status < http.StatusBadRequest {
				return
			}
			status := response.status
			if status == 0 {
				status = http.StatusOK
			}
			outcome := "success"
			if status >= http.StatusInternalServerError {
				outcome = "error"
			} else if status >= http.StatusBadRequest {
				outcome = "rejected"
			}
			var userID int64
			if response.session != nil {
				userID = response.session.UserID
			}
			details := []slog.Attr{slog.Int("status", status)}
			resources := map[string]string{}
			for _, key := range []string{"id", "post", "comment", "event", "album", "image", "module", "submission"} {
				if value := r.PathValue(key); value != "" {
					resources[key] = value
				}
			}
			if len(resources) != 0 {
				details = append(details, slog.Any("resources", resources))
			}
			audit.Audit(ctx, "http.request", outcome, userID, details...)
		}()
		var s *session
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			var err error
			s, err = c.identity.Session(r.Context(), cookie.Value)
			if err != nil {
				c.serverError(w, err)
				return
			}
		}
		if s == nil && policy.session && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			var err error
			s, err = c.identity.CreateSession(r.Context(), 0)
			if err != nil {
				c.serverError(w, err)
				return
			}
			c.setSessionCookie(w, r, s)
		}
		response.session = s
		if s != nil && s.Authenticated {
			ctx = audit.WithActor(ctx, s.UserID)
			r = r.WithContext(ctx)
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			response.canonicalURL = canonicalURL(r)
		}
		if policy.authenticated || policy.session || s != nil {
			w.Header().Set("Cache-Control", "no-store")
		}
		if policy.private {
			w.Header().Set("Cache-Control", "private, no-store")
		}
		w.Header().Add("Vary", "Cookie")
		if policy.authenticated && (s == nil || !s.Authenticated) {
			audit.Audit(ctx, "access.denied", "login_required", 0)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if policy.admin && !s.CanAdmin() || policy.reviewer && !s.CanReview() {
			audit.Audit(ctx, "access.denied", "insufficient_role", s.UserID)
			c.pageError(response, "Du hast keine Berechtigung für die Verwaltung.", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			if policy.bodyLimit > 0 {
				r.Body = http.MaxBytesReader(w, r.Body, policy.bodyLimit)
			}
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				if err := r.ParseMultipartForm(policy.bodyLimit); err != nil {
					c.pageError(response, "Das Formular ist zu groß oder konnte nicht gelesen werden.", http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
			} else if err := r.ParseForm(); err != nil {
				c.pageError(response, "Das Formular konnte nicht gelesen werden.", http.StatusBadRequest)
				return
			}
			if policy.csrf && !validCSRF(s, r.PostForm.Get("csrf")) {
				var userID int64
				if s != nil {
					userID = s.UserID
				}
				audit.Audit(ctx, "request.csrf", "rejected", userID)
				c.pageError(response, "Lade die Seite neu und versuche es noch einmal.", http.StatusForbidden)
				return
			}
		}
		if s != nil && s.Authenticated && !policy.passive {
			if err := c.identity.TouchSession(r.Context(), s); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					http.Redirect(response, r, "/session/expired", http.StatusSeeOther)
				} else {
					c.serverError(response, err)
				}
				return
			}
		}
		next.ServeHTTP(response, r)
	})
}

func validCSRF(s *session, value string) bool {
	return s != nil && value != "" && subtle.ConstantTimeCompare([]byte(s.CSRF), []byte(value)) == 1
}

func (c *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if response := w.(*sessionResponse); response.session != nil {
		if err := c.identity.DeleteSession(r.Context(), response.session.ID); err != nil {
			c.serverError(w, err)
			return
		}
		audit.Audit(r.Context(), "account.logout", "success", response.session.UserID)
		transient.Lock()
		delete(transient.flash, response.session.ID)
		transient.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: c.secureCookies || r.TLS != nil, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
