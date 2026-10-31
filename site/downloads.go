package site

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/model/events"
	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/model/media"
	"github.com/fachschaftinformatik/web/storage"
)

func (c *Handler) HistoricalDocument(w http.ResponseWriter, r *http.Request) {
	object, err := c.meetings.HistoricalDocumentByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, err)
		return
	}
	defer object.Close()
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if seeker, ok := object.ReadCloser.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "dokument.pdf", time.Time{}, seeker)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(object.Info.Size, 10))
	if r.Method != http.MethodHead {
		if _, err := io.CopyN(w, object, object.Info.Size); err != nil {
			log.Printf("Historical document: %v", err)
		}
	}
}

func (c *Handler) Document(w http.ResponseWriter, r *http.Request) {
	internal, err := c.meetings.ResolvePublicID(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		c.HistoricalDocument(w, r)
		return
	}
	if err != nil {
		c.serverError(w, err)
		return
	}
	p, ok, err := c.meetings.GetWithError(internal)
	if err != nil {
		c.serverError(w, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	s := w.(*sessionResponse).session
	if !ok || !p.Published && (s == nil || !s.Authenticated || c.publicationOwner(w) != p.Owner) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="dokument.pdf"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "dokument.pdf", time.Time{}, bytes.NewReader(p.Data))
}

func (c *Handler) ExamsFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	response := w.(*sessionResponse)
	if response.session == nil || !response.session.Authenticated {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	object, err := c.exams.Open(r.Context(), response.session.UserID, r.PathValue("id"))
	if errors.Is(err, exams.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, exams.ErrForbidden) {
		http.Error(w, "Melde dich mit einem aktiven, bestätigten Konto an.", http.StatusForbidden)
		return
	}
	if errors.Is(err, exams.ErrStorage) {
		log.Printf("Exams storage: %v", err)
		http.Error(w, "Die Datei konnte nicht geladen werden. Versuche es später erneut.", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		c.serverError(w, err)
		return
	}
	defer object.Close()
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	audit.Audit(r.Context(), "exam_document.access", "granted", response.session.UserID, slog.String("document_id", r.PathValue("id")))
	w.Header().Set("Content-Disposition", `inline; filename="altklausur.pdf"`)
	if seeker, ok := object.ReadCloser.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "altklausur.pdf", time.Time{}, seeker)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(object.Info.Size, 10))
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.CopyN(w, object, object.Info.Size); err != nil {
		log.Printf("Exams download: %v", err)
	}
}

func (c *Handler) CalendarCover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	object, err := c.events.OpenCover(r.Context(), r.PathValue("event"))
	if err != nil {
		if errors.Is(err, events.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Das Titelbild konnte nicht geladen werden.", http.StatusServiceUnavailable)
		}
		return
	}
	c.serveMediaObject(w, r, object)
}

// MediaFile serves only database-backed images, never raw storage keys.
func (c *Handler) MediaFile(w http.ResponseWriter, r *http.Request) {
	c.mediaImageFile(w, r, false)
}

func (c *Handler) AlbumCover(w http.ResponseWriter, r *http.Request) {
	c.mediaImageFile(w, r, true)
}

func (c *Handler) mediaImageFile(w http.ResponseWriter, r *http.Request, cover bool) {
	w.Header().Set("Cache-Control", "no-store")

	open := c.media.Open
	if cover {
		open = c.media.OpenCover
	}
	object, err := open(r.Context(), r.PathValue("id"), r.PathValue("size"))
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, media.ErrStorage) {
		http.Error(w, "Das Bild konnte nicht geladen werden.", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		c.serverError(w, err)
		return
	}
	c.serveMediaObject(w, r, object)
}

func (c *Handler) serveMediaObject(w http.ResponseWriter, r *http.Request, object *storage.Object) {
	defer object.Close()
	seeker, ok := object.ReadCloser.(io.ReadSeeker)
	if !ok {
		// Providers may return streaming bodies. Spool those to a bounded
		// temporary file so ServeContent still supports HEAD and byte ranges.
		file, err := os.CreateTemp("", "media")
		if err != nil {
			c.serverError(w, err)
			return
		}
		defer os.Remove(file.Name())
		defer file.Close()
		written, err := io.Copy(file, io.LimitReader(object, object.Info.Size+1))
		if err != nil || written != object.Info.Size {
			http.Error(w, "Das Bild konnte nicht geladen werden.", http.StatusServiceUnavailable)
			return
		}
		seeker = file
	}
	w.Header().Set("Content-Type", object.Info.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Keep photographs in the browser briefly, rather than in shared caches.
	// Validators let refreshes reuse the body and allow mutable covers to change.
	w.Header().Set("Cache-Control", "private, max-age=300, must-revalidate")
	if object.Info.ETag != "" {
		w.Header().Set("ETag", object.Info.ETag)
	}
	http.ServeContent(w, r, "image", object.Info.Modified, seeker)
}
