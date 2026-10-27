package members

import (
	"context"
	"database/sql"
	"log"
	"net/mail"
	"slices"
	"strings"
	"sync"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/model/users"
)

type TeamMember struct {
	Name, Initials, Role, Email string
}

type TeamSection struct {
	Title, Group string
	Members      []TeamMember
}

type Group struct {
	ID, Title string
	Sections  []TeamSection
}

// SharedEmail avoids repeating a role's shared address for each member.
func (s TeamSection) SharedEmail() string {
	if len(s.Members) < 2 || s.Members[0].Email == "" {
		return ""
	}
	email := s.Members[0].Email
	for _, member := range s.Members[1:] {
		if member.Email != email {
			return ""
		}
	}
	return email
}

type Member struct {
	ID, Name, Email string
	PublicID        string
	UserID          int64
	UserPublicID    string
	AccountEmail    string
	Confirmed       bool
	AccessRole      string
}

type MemberRole struct {
	ID, Group, Title, Email string
	PublicID                string
	MemberIDs               []string
	OrganizationID          int64
	Organization            string
}

// Store shares public profiles and assignments between public and admin views.
type Store struct {
	db      *sql.DB
	mu      sync.RWMutex
	members []Member
	roles   []MemberRole
}

func (s *Store) Members() []Member {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.members)
}

func (s *Store) AssignableMembers() []Member {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Member
	for _, u := range s.members {
		assigned := u.AccessRole == "editor" || u.AccessRole == "admin"
		for _, f := range s.roles {
			if slices.Contains(f.MemberIDs, u.ID) {
				assigned = true
				break
			}
		}
		if assigned {
			result = append(result, u)
		}
	}
	return result
}

func (s *Store) Roles(group string) []MemberRole {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []MemberRole
	for _, r := range s.roles {
		if r.Group == group {
			r.MemberIDs = slices.Clone(r.MemberIDs)
			result = append(result, r)
		}
	}
	return result
}

func validEmail(value string) bool {
	if value == "" {
		return true
	}
	a, err := mail.ParseAddress(value)
	return err == nil && a.Address == value
}

func (s *Store) SaveMember(p Member) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	errors := map[string]string{}
	p.Name, p.Email = strings.TrimSpace(p.Name), strings.TrimSpace(p.Email)
	if message := users.NameError(p.Name); message != "" {
		errors["name"] = message
	}
	if !validEmail(p.Email) {
		errors["email"] = "Gib eine gültige E-Mail-Adresse ein."
	}
	if len(errors) != 0 {
		return errors
	}
	if p.ID == "create" {
		return map[string]string{"name": "Lade ein Benutzerkonto ein."}
	}
	for i := range s.members {
		if s.members[i].ID == p.ID {
			old := s.members[i]
			p.PublicID = old.PublicID
			// Account linkage is not editable through public profile fields.
			p.UserID, p.UserPublicID, p.AccountEmail, p.Confirmed = old.UserID, old.UserPublicID, old.AccountEmail, old.Confirmed
			if old.UserID != 0 {
				if err := s.saveLinked(context.Background(), p); err != nil {
					log.Printf("Save member: %v", err)
					return map[string]string{"name": "Das Mitglied konnte nicht gespeichert werden."}
				}
				s.members[i] = p
				return nil
			}
			return map[string]string{"name": "Das Benutzerkonto wurde nicht gefunden."}
		}
	}
	return map[string]string{"name": "Dieses Mitglied wurde nicht gefunden."}
}

func (s *Store) SaveRole(r MemberRole) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	errors := map[string]string{}
	r.Title, r.Email = strings.TrimSpace(r.Title), strings.TrimSpace(r.Email)
	if r.Title == "" {
		errors["title"] = "Gib eine Bezeichnung ein."
	}
	if !validEmail(r.Email) {
		errors["email"] = "Gib eine gültige E-Mail-Adresse ein."
	}
	if r.Group != "board" && r.Group != "departments" && r.Group != "project" {
		errors["group"] = "Wähle Vorstand, Referat oder Projektstelle aus."
	}
	if r.OrganizationID == 0 {
		r.OrganizationID = 1
	}
	var organizationExists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM organizations WHERE id=?)`, r.OrganizationID).Scan(&organizationExists); err != nil || !organizationExists {
		errors["organization"] = "Wähle eine vorhandene Organisation aus."
	}
	for _, id := range r.MemberIDs {
		if !slices.ContainsFunc(s.members, func(p Member) bool { return p.ID == id }) {
			errors["members"] = "Wähle vorhandene Mitglieder aus."
		}
	}
	if len(errors) != 0 {
		return errors
	}
	var members []string
	for _, member := range r.MemberIDs {
		if !slices.Contains(members, member) {
			members = append(members, member)
		}
	}
	r.MemberIDs = members
	if r.ID == "create" {
		r.ID = publicid.New()
		r.PublicID = r.ID
		s.roles = append(s.roles, r)
		if err := s.persist(); err != nil {
			s.roles = s.roles[:len(s.roles)-1]
			log.Printf("Save role: %v", err)
			return map[string]string{"title": "Die Funktion konnte nicht gespeichert werden."}
		}
		return nil
	}
	for i, old := range s.roles {
		if old.ID == r.ID {
			r.PublicID = old.PublicID
			s.roles[i] = r
			if err := s.persist(); err != nil {
				s.roles[i] = old
				log.Printf("Save role: %v", err)
				return map[string]string{"title": "Die Funktion konnte nicht gespeichert werden."}
			}
			return nil
		}
	}
	return map[string]string{"title": "Dieser Eintrag wurde nicht gefunden."}
}

func (s *Store) Groups() []Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	groups := []Group{{ID: "board", Title: "Vorstand"}, {ID: "departments", Title: "Referate"}, {ID: "project", Title: "Projektstellen"}}
	for i := range groups {
		for _, r := range s.roles {
			if r.Group != groups[i].ID || r.OrganizationID != 1 {
				continue
			}
			section := TeamSection{Title: r.Title}
			if r.Group == "board" {
				section.Title = "Vorstand"
			}
			for _, id := range r.MemberIDs {
				for _, p := range s.members {
					if p.ID != id {
						continue
					}
					email, role := p.Email, r.Title
					if r.Email != "" {
						email = r.Email
					}
					section.Members = append(section.Members, TeamMember{Name: p.Name, Role: role, Email: email})
				}
			}
			groups[i].Sections = append(groups[i].Sections, section)
		}
	}
	return groups
}

func (s *Store) persist() error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for position, r := range s.roles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO functions(id,group_name,title,email,position,organization_id) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET group_name=excluded.group_name,title=excluded.title,email=excluded.email,position=excluded.position,organization_id=excluded.organization_id`, r.ID, r.Group, r.Title, r.Email, position, r.OrganizationID); err != nil {
			return err
		}
		value, err := publicid.Ensure(ctx, tx, "roles", r.ID)
		if err != nil {
			return err
		}
		s.roles[position].PublicID = value
		rows, err := tx.QueryContext(ctx, `SELECT CAST(user_id AS TEXT) FROM user_functions WHERE function_id=? ORDER BY position,user_id`, r.ID)
		if err != nil {
			return err
		}
		var current []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			current = append(current, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if slices.Equal(current, r.MemberIDs) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_functions WHERE function_id=?`, r.ID); err != nil {
			return err
		}
		for position, id := range r.MemberIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_functions(user_id,function_id,position) VALUES(?,?,?)`, id, r.ID, position); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
