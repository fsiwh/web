package audit

import (
	"context"
	"database/sql"
	"slices"
	"strings"
)

const PerPage = 20

type Account struct {
	ID             int64
	Name, PublicID string
}

type Entry struct {
	OccurredAt, Event, Outcome string
	Actor, User                Account
	Content, ContentURL        string
}

type Page struct {
	Entries          []Entry
	Total            int
	Filter           Filter
	Events, Outcomes []string
}

type Filter struct {
	Query, Event, Outcome string
}

const accountJoins = ` FROM audit_events e
	LEFT JOIN users a ON a.id=e.actor_user_id AND a.deleted_at IS NULL
	LEFT JOIN public_ids ap ON ap.namespace='users' AND ap.record_id=CAST(a.id AS TEXT) AND ap.retired=0
	LEFT JOIN users u ON u.id=e.user_id AND u.deleted_at IS NULL
	LEFT JOIN public_ids up ON up.namespace='users' AND up.record_id=CAST(u.id AS TEXT) AND up.retired=0 `

const filterWhere = ` WHERE (?='' OR e.event=?) AND (?='' OR e.outcome=?)
	AND (?='' OR instr(lower(e.event),?)>0
	OR instr(lower(coalesce(a.name,'')),?)>0 OR instr(lower(coalesce(a.email,'')),?)>0
	OR instr(lower(coalesce(u.name,'')),?)>0 OR instr(lower(coalesce(u.email,'')),?)>0) `

// List reads a bounded page without changing the append-only history. Accounts
// are optional: events remain readable after the associated account is deleted.
func (s *Store) List(ctx context.Context, filter Filter, number int) (Page, error) {
	var page Page
	if number < 1 {
		return page, sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	options, err := tx.QueryContext(ctx, "SELECT DISTINCT event,outcome FROM audit_events ORDER BY event,outcome")
	if err != nil {
		return page, err
	}
	for options.Next() {
		var event, outcome string
		if err := options.Scan(&event, &outcome); err != nil {
			options.Close()
			return page, err
		}
		if !slices.Contains(page.Events, event) {
			page.Events = append(page.Events, event)
		}
		if !slices.Contains(page.Outcomes, outcome) {
			page.Outcomes = append(page.Outcomes, outcome)
		}
	}
	err = options.Err()
	options.Close()
	if err != nil {
		return page, err
	}
	slices.Sort(page.Outcomes)
	filter.Query = strings.TrimSpace(filter.Query)
	if !slices.Contains(page.Events, filter.Event) {
		filter.Event = ""
	}
	if !slices.Contains(page.Outcomes, filter.Outcome) {
		filter.Outcome = ""
	}
	page.Filter = filter
	query := strings.ToLower(filter.Query)
	args := []any{filter.Event, filter.Event, filter.Outcome, filter.Outcome, query, query, query, query, query, query}
	if err := tx.QueryRowContext(ctx, "SELECT count(*)"+accountJoins+filterWhere, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	if number > max(1, (page.Total+PerPage-1)/PerPage) {
		return page, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.occurred_at,e.event,e.outcome,
		coalesce(e.actor_user_id,0),coalesce(a.name,''),coalesce(ap.public_id,''),
		coalesce(e.user_id,0),coalesce(u.name,''),coalesce(up.public_id,'')
		`+accountJoins+filterWhere+`ORDER BY e.occurred_at DESC,e.id DESC LIMIT ? OFFSET ?`, append(args, PerPage, (number-1)*PerPage)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.OccurredAt, &e.Event, &e.Outcome, &e.Actor.ID, &e.Actor.Name, &e.Actor.PublicID, &e.User.ID, &e.User.Name, &e.User.PublicID); err != nil {
			return page, err
		}
		page.Entries = append(page.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	return page, tx.Commit()
}
