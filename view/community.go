package view

import (
	"html/template"
	"net/url"
	"strings"

	"github.com/fachschaftinformatik/web/internal/content"

	"github.com/fachschaftinformatik/web/internal/urls"
)

type MeetingsPage struct {
	Year     int
	Years    []int
	Meetings []Meeting
	Next     *Meeting
}

type ContentEditor struct {
	Draft                EditorDraft
	Errors               []content.FieldError
	Editing, Unavailable bool
	Action, BackURL      string
	RichBody             string
	Event, Announcement  bool
	CoverURL, CoverAlt   string
}

func (e ContentEditor) RichHTML() template.HTML {
	return template.HTML(content.SanitizeRichText(e.RichBody))
}

func (e ContentEditor) Error(field string) string {
	for _, err := range e.Errors {
		if err.Field == field {
			return err.Message
		}
	}
	return ""
}

func (e ContentEditor) DateDay() string   { return e.datePart(0) }
func (e ContentEditor) DateMonth() string { return e.datePart(1) }
func (e ContentEditor) DateYear() string  { return e.datePart(2) }

func (e ContentEditor) datePart(index int) string {
	parts := strings.Split(e.Draft.Date, ".")
	if index < len(parts) {
		return parts[index]
	}
	return ""
}

type EventsPage struct {
	Pagination     ListPagination
	Upcoming, Past []Event
	Detail         *Event
	CanCreate      bool
}

type PostsPage struct {
	Pagination        ListPagination
	CanWrite, CanEdit bool
	Filter            PostsFilter
	Posts             []Post
	Thread            *Thread
	BackURL           string
	Comment           CommentForm
}

type CommentForm struct {
	ID, Text, Error string
}

func (p PostsPage) SearchURL() string {
	if p.Filter.Query == "" {
		return urls.PostsPath
	}
	return urls.PostsPath + "?" + url.Values{"search": {p.Filter.Query}}.Encode()
}

func (f CommentForm) Anchor() string {
	if f.ID != "" {
		return "edit-comment-" + f.ID
	}
	return "comment-text"
}

func (p PostsPage) CommentURL(id string) string {
	base := p.Thread.URL()
	if p.Pagination.Page > 1 {
		base = p.Pagination.PageURL(p.Pagination.Page)
	}
	return base + "#comment-" + id
}

type HomePost struct {
	Title, Summary, URL, VoteURL, Date, DateISO string
	Votes, UserVote                             int
}

func HomeItem(p Post) HomePost {
	return HomePost{Title: p.Title, Summary: p.Summary(), URL: p.URL(), VoteURL: p.VoteURL(), Date: p.DateLabel(), DateISO: p.DateISO(), Votes: p.Votes, UserVote: p.UserVote}
}

type HomePage struct {
	Posts, Announcements []HomePost
	Events               []Event
}

type MediaPage struct {
	CanManage                  bool
	Managing, Saved            bool
	GalleryItems               []GalleryItem
	Pagination                 ListPagination
	Albums                     []Album
	Album                      Album
	Images                     []Image
	Image                      Image
	Detail, Viewer             bool
	Number, Total, Page, Pages int
	PageNumbers                []int
	Previous, Next, Return     string
	PageSize                   int
}

func (m MediaPage) ImageNumber(index int) int {
	return (m.Page-1)*m.PageSize + index + 1
}

func (m MediaPage) CarouselAlbums() []Album {
	var albums []Album
	for _, album := range m.Albums {
		if album.CoverImage().ID != "" {
			albums = append(albums, album)
		}
	}
	return albums
}

type VoteControl struct {
	Page                  Page
	Action, Return, Label string
	Score, Selected       int
	Up, Down              int
}

func voteControl(page Page, action, back, label string, score, selected int) VoteControl {
	v := VoteControl{Page: page, Action: action, Return: back, Label: label, Score: score, Selected: selected, Up: 1, Down: -1}
	if selected == 1 {
		v.Up = 0
	}
	if selected == -1 {
		v.Down = 0
	}
	return v
}

type EditorDraft struct{ Title, Body, Type, Tags, Date, Time, Location string }
