package site

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) SessionStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	s := w.(*sessionResponse).session
	if s == nil || !s.Authenticated {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		Deadline int64 `json:"deadline"`
		Absolute int64 `json:"absolute"`
		Now      int64 `json:"now"`
	}{s.Deadline().UnixMilli(), s.Expires.UnixMilli(), time.Now().UnixMilli()})
}

func (c *Handler) ExtendSession(w http.ResponseWriter, r *http.Request) {
	s := w.(*sessionResponse).session
	if s == nil || !s.Authenticated {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err := c.identity.TouchSession(r.Context(), s); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			c.serverError(w, err)
		}
		return
	}
	c.SessionStatus(w, r)
}

func (c *Handler) SessionExpired(w http.ResponseWriter, r *http.Request) {
	c.render(w, "sessionexpired", false, view.Page{Title: "Deine Sitzung ist abgelaufen", Active: "session-expired"})
}
