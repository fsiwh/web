// Package organization reads the site's editable identity and contact data.
package organization

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

type Link struct{ Title, URL string }

type Details struct {
	Name, FormalName, ShortName, Institution    string
	City, Street, PostalCode, Country, Office   string
	ContactEmail                                string
	ChairRole                                   string `json:"ChairResponsibility"`
	PublicityRole                               string `json:"PublicityResponsibility"`
	LegalUpdated                                string
	HostingName, HostingURL, PrivacyURL         string
	SupervisionName, SupervisionURL             string
	PrivacyAuthorityName, PrivacyAuthorityURL   string
	SocialLinks, ElectionLinks, RegulationLinks []Link
}

func Load(ctx context.Context, db *sql.DB) (Details, error) {
	var raw string
	var d Details
	if err := db.QueryRowContext(ctx, "SELECT data FROM organization WHERE id=1").Scan(&raw); err != nil {
		return d, fmt.Errorf("load organization settings: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return d, err
	}
	if fields := d.Validate(); len(fields) > 0 {
		return d, fmt.Errorf("invalid organization settings: %v", fields)
	}
	return d, nil
}

func (d Details) Validate() map[string]string {
	fields := map[string]string{}
	for key, value := range map[string]string{"Name": d.Name, "FormalName": d.FormalName, "ShortName": d.ShortName, "Institution": d.Institution, "Street": d.Street, "PostalCode": d.PostalCode, "City": d.City, "Country": d.Country, "HostingName": d.HostingName, "SupervisionName": d.SupervisionName, "PrivacyAuthorityName": d.PrivacyAuthorityName} {
		if strings.TrimSpace(value) == "" {
			fields[key] = "Gib eine Bezeichnung an."
		} else if len([]rune(value)) > 255 {
			fields[key] = "Verwende höchstens 255 Zeichen."
		}
	}
	if a, err := mail.ParseAddress(d.ContactEmail); err != nil || a.Address != d.ContactEmail {
		fields["ContactEmail"] = "Gib eine gültige E-Mail-Adresse ein."
	}
	if _, err := time.Parse(time.DateOnly, d.LegalUpdated); err != nil {
		fields["LegalUpdated"] = "Gib ein gültiges Datum im Format JJJJ-MM-TT ein."
	}
	validURL := func(value string) bool {
		u, err := url.Parse(value)
		return err == nil && u.Host != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http")
	}
	for key, value := range map[string]string{"HostingURL": d.HostingURL, "PrivacyURL": d.PrivacyURL, "SupervisionURL": d.SupervisionURL, "PrivacyAuthorityURL": d.PrivacyAuthorityURL} {
		if !validURL(value) {
			fields[key] = "Gib eine vollständige HTTP- oder HTTPS-Adresse ein."
		}
	}
	for key, links := range map[string][]Link{"SocialLinks": d.SocialLinks, "ElectionLinks": d.ElectionLinks, "RegulationLinks": d.RegulationLinks} {
		if len(links) > 30 {
			fields[key] = "Verwende höchstens 30 Verknüpfungen."
		}
		for _, link := range links {
			if strings.TrimSpace(link.Title) == "" || len([]rune(link.Title)) > 255 || !validURL(link.URL) {
				fields[key] = "Gib für jede Verknüpfung eine Bezeichnung und eine vollständige HTTP- oder HTTPS-Adresse ein."
			}
		}
	}
	return fields
}

type Store struct {
	db      *sql.DB
	mu      sync.RWMutex
	details Details
}

func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	d, err := Load(ctx, db)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, details: d}, nil
}

func clone(d Details) Details {
	d.SocialLinks = slices.Clone(d.SocialLinks)
	d.ElectionLinks = slices.Clone(d.ElectionLinks)
	d.RegulationLinks = slices.Clone(d.RegulationLinks)
	return d
}

func (s *Store) Current() Details { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.details) }

// Read includes revision updates made by changes to linked legal contacts.
func (s *Store) Read(ctx context.Context) (Details, error) { return Load(ctx, s.db) }

var ErrForbidden = errors.New("insufficient permission for these organization settings")

// Save authorizes and commits the complete record before publishing a new snapshot.
func (s *Store) Save(ctx context.Context, user int64, d Details) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var role string
	if err = tx.QueryRowContext(ctx, "SELECT role FROM users WHERE id=? AND active=1 AND verified=1 AND role IN ('admin','editor') AND deleted_at IS NULL", user).Scan(&role); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrForbidden
	} else if err != nil {
		return nil, err
	}
	// The revision date is derived from changes to the information used by
	// legal pages, never from a submitted date or an unchanged form save.
	var rawBefore string
	if err := tx.QueryRowContext(ctx, "SELECT data FROM organization WHERE id=1").Scan(&rawBefore); err != nil {
		return nil, err
	}
	var before Details
	if err := json.Unmarshal([]byte(rawBefore), &before); err != nil {
		return nil, err
	}
	if role == "editor" {
		// Only editorial fields may differ, even when the service is called
		// directly or an old form submits a stale complete record.
		protected := d
		protected.City, protected.Street, protected.PostalCode, protected.Country, protected.Office = before.City, before.Street, before.PostalCode, before.Country, before.Office
		protected.ContactEmail, protected.ChairRole, protected.PublicityRole = before.ContactEmail, before.ChairRole, before.PublicityRole
		protected.SocialLinks, protected.ElectionLinks = before.SocialLinks, before.ElectionLinks
		protected.LegalUpdated = before.LegalUpdated
		if !reflect.DeepEqual(protected, before) {
			return nil, ErrForbidden
		}
	}
	d.LegalUpdated = before.LegalUpdated
	after := d
	before.SocialLinks, after.SocialLinks = nil, nil
	before.ElectionLinks, after.ElectionLinks = nil, nil
	if !reflect.DeepEqual(before, after) {
		d.LegalUpdated = time.Now().UTC().Format(time.DateOnly)
	}
	fields := d.Validate()
	for key, value := range map[string]string{"ChairRole": d.ChairRole, "PublicityRole": d.PublicityRole} {
		group := "board"
		if key == "PublicityRole" {
			group = "departments"
		}
		if value == "" {
			continue
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM functions WHERE id=? AND group_name=? AND organization_id=1", value, group).Scan(&count); err != nil {
			return nil, err
		}
		if count != 1 {
			fields[key] = "Wähle eine vorhandene Funktion aus."
		}
	}
	if len(fields) > 0 {
		return fields, nil
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, "UPDATE organization SET data=? WHERE id=1", string(raw))
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, sql.ErrNoRows
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.details = clone(d)
	return nil, nil
}
