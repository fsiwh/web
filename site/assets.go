package site

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/fachschaftinformatik/web/view"
)

// browserAssets is session-independent. Versioned bundle URLs are immutable;
// legacy URLs revalidate and remain available to existing documents.
func browserAssets() http.Handler {
	assets := view.Assets()
	version := view.AssetVersion()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		versioned := strings.HasPrefix(name, version+"/")
		if versioned {
			name = strings.TrimPrefix(name, version+"/")
		}
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if versioned && (strings.HasSuffix(name, ".css") || strings.HasSuffix(name, ".js")) {
			// Absolute local dependencies join the same immutable bundle namespace;
			// relative dependencies already resolve there through the document URL.
			data = bytes.ReplaceAll(data, []byte("/assets/"), []byte("/assets/"+version+"/"))
		}
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(data)))
		if versioned {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, no-cache")
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}
