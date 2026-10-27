package users

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/fachschaftinformatik/web/internal/publicid"
)

// User is the account directory projection of a login identity.
type User struct {
	ID                int64
	Name, Email, Role string
	PublicID          string
	Program           string
	Degree            string
	Active            bool
	Verified          bool
	InvitationPending bool
}

// Directory is an immutable account display snapshot. It contains neither
// credentials nor authentication decisions.
type Directory struct {
	users []User
}

func NewDirectory(records []User) *Directory {
	records = slices.Clone(records)
	for i := range records {
		records[i].Email = strings.ToLower(records[i].Email)
	}
	return &Directory{users: records}
}

func (d *Directory) Get(id string) (User, bool) {
	for _, user := range d.users {
		if publicid.Valid(id) && user.PublicID == id {
			return user, true
		}
	}
	return User{}, false
}

const PerPage = 20

type Page struct {
	Users                []User
	Number, Pages, Total int
}

type DirectoryFilter struct {
	Query  string
	Roles  []string
	Status string
}

// Search applies the shared account search and page bounds. Empty results have
// one valid, empty page. Out-of-range page numbers are rejected, not clamped.
func (d *Directory) Search(query string, number int) (Page, bool) {
	return d.Filter(DirectoryFilter{Query: query}, number)
}

func (d *Directory) Filter(filter DirectoryFilter, number int) (Page, bool) {
	records := search(d.users, filter.Query)
	records = slices.DeleteFunc(records, func(user User) bool {
		if len(filter.Roles) > 0 && !slices.Contains(filter.Roles, user.Role) {
			return true
		}
		switch filter.Status {
		case "verified":
			return !user.Verified
		case "unverified":
			return user.Verified
		case "active":
			return !user.Active
		case "inactive":
			return user.Active
		}
		return false
	})
	page := Page{Number: number, Pages: max(1, (len(records)+PerPage-1)/PerPage), Total: len(records)}
	if number < 1 || number > page.Pages {
		return page, false
	}
	start := (number - 1) * PerPage
	page.Users = records[start:min(start+PerPage, len(records))]
	return page, true
}

func search(all []User, query string) []User {
	var result []User
	query = strings.ToLower(strings.TrimSpace(query))
	for _, user := range all {
		if strings.Contains(strings.ToLower(user.Name+" "+user.Email), query) {
			result = append(result, user)
		}
	}
	return result
}

type Program struct{ ID, Name string }

var ErrProfileNotFound = errors.New("public profile not found")

// PublicProfile contains only information a user has chosen to make public.
type PublicProfile struct {
	ID       int64
	PublicID string
	Name     string
}

func (s *Service) PublicProfile(ctx context.Context, value string) (PublicProfile, error) {
	var profile PublicProfile
	id, err := s.ResolvePublicID(ctx, value)
	if errors.Is(err, sql.ErrNoRows) {
		return profile, ErrProfileNotFound
	}
	if err != nil {
		return profile, err
	}
	profile.PublicID = value
	err = s.db.QueryRowContext(ctx, `SELECT id,name FROM users WHERE id=? AND private=0 AND deleted_at IS NULL`, id).Scan(&profile.ID, &profile.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return profile, ErrProfileNotFound
	}
	return profile, err
}

func (s *Service) PublicID(ctx context.Context, internal int64) (string, error) {
	return publicid.Ensure(ctx, s.db, "users", strconv.FormatInt(internal, 10))
}

func (s *Service) ResolvePublicID(ctx context.Context, value string) (int64, error) {
	if !publicid.Valid(value) {
		return 0, sql.ErrNoRows
	}
	internal, err := publicid.Resolve(ctx, s.db, "users", value)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(internal, 10, 64)
}
