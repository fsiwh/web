package site

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/fachschaftinformatik/web/model/meetings"
	"github.com/fachschaftinformatik/web/model/posts"
	"github.com/fachschaftinformatik/web/view"
)

const uploadBodyLimit = meetings.MaxPDFBytes + 1<<20

func (c *Handler) home(ctx context.Context, viewer ...int64) (view.HomePage, error) {
	calendar, _, err := c.events.FindPage(ctx, time.Now(), 1, 4, false)
	if err != nil {
		return view.HomePage{}, err
	}

	records, _, err := c.posts.FindPage(ctx, posts.Filter{Type: "discussion"}, 1, 2, viewer...)
	if err != nil {
		return view.HomePage{}, err
	}
	page := view.HomePage{Events: mapRecords(calendar, eventView)}
	for _, post := range records {
		item := view.HomeItem(postView(post))
		if post.Type == "discussion" && len(page.Posts) < 2 {
			page.Posts = append(page.Posts, item)
		}
	}
	return page, nil
}

func (c *Handler) Legal(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimPrefix(r.URL.Path, "/")
	titles := map[string]string{"legal": "Impressum", "privacy": "Datenschutz", "accessibility": "Barrierefreiheit"}
	if err := c.members.Refresh(r.Context()); err != nil {
		c.serverError(w, err)
		return
	}
	legal := view.LegalPage{Kind: kind}
	org, err := c.organization.Read(r.Context())
	if err != nil {
		c.serverError(w, err)
		return
	}
	legal.Updated = org.LegalUpdated
	for _, role := range c.members.Roles("board") {
		if role.ID == org.ChairRole {
			for _, person := range c.members.Members() {
				if slices.Contains(role.MemberIDs, person.ID) {
					legal.Chair = append(legal.Chair, memberView(person))
				}
			}
		}
	}
	c.render(w, "legal", false, view.Page{Title: titles[kind], Active: "legal", Legal: legal})
}

func (c *Handler) Sitemap(w http.ResponseWriter, r *http.Request) {
	albums, total, err := c.media.AlbumPage(r.Context(), requestedPage(r), 100)
	if err != nil {
		c.serverError(w, err)
		return
	}

	if !c.validPageRange(w, r, total, 100) {
		return
	}
	c.render(w, "sitemap", false, view.Page{Title: "Seitenübersicht", Active: "sitemap", Media: view.MediaPage{Albums: mapRecords(albums, albumView), Pagination: listPagination(r, total, 100)}})
}
