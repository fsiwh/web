package audit

import (
	"context"
	"database/sql"
	"slices"
	"strings"
)

// An explicit allowlist keeps security events and private drafts out of every
// editorial result, including counts, filter options and search matches. This
// projection never joins accounts or reads arbitrary event details.
const editorialEvents = `WITH editorial AS (
 SELECT e.id,e.occurred_at,e.event,e.outcome,
 CASE WHEN e.event IN ('submission.submitted','submission.reviewed','submission.edited')
 THEN COALESCE((SELECT group_concat(DISTINCT m.name) FROM archive a JOIN modules m ON m.id=a.module_id WHERE a.request_id=r.id AND a.deleted_at IS NULL),'Einreichung')
 ELSE COALESCE(json_extract(e.details,'$.content_label'),'') END AS content,
 CASE WHEN e.event IN ('submission.submitted','submission.reviewed','submission.edited') THEN '/admin/submissions/' || ref.public_id
 ELSE COALESCE(json_extract(e.details,'$.content_url'),'') END AS content_url
 FROM audit_events e
 LEFT JOIN public_ids ref ON ref.namespace='submissions' AND ref.public_id=json_extract(e.details,'$.submission_id') AND ref.retired=0
 LEFT JOIN archive_requests r ON CAST(r.id AS TEXT)=ref.record_id
 WHERE (e.event IN ('submission.submitted','submission.reviewed','submission.edited') AND r.status!='draft')
 OR e.event IN ('content.news_saved','content.discussion_saved','content.comment_edited','content.event_saved','content.meeting_published','content.photo_visibility','content.member_saved','content.function_saved','content.settings_saved')
) `

func (s *Store) EditorialList(ctx context.Context, filter Filter, number int) (Page, error) {
	var page Page
	if number < 1 {
		return page, sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	options, err := tx.QueryContext(ctx, editorialEvents+"SELECT DISTINCT event,outcome FROM editorial ORDER BY event,outcome")
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
	where := ` WHERE (?='' OR event=?) AND (?='' OR outcome=?) AND (?='' OR instr(lower(event),?)>0 OR instr(lower(content),?)>0)`
	query := strings.ToLower(filter.Query)
	args := []any{filter.Event, filter.Event, filter.Outcome, filter.Outcome, query, query, query}
	if err := tx.QueryRowContext(ctx, editorialEvents+"SELECT count(*) FROM editorial"+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.QueryContext(ctx, editorialEvents+"SELECT occurred_at,event,outcome,content,content_url FROM editorial"+where+" ORDER BY occurred_at DESC,id DESC LIMIT ? OFFSET ?", append(args, PerPage, (number-1)*PerPage)...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.OccurredAt, &entry.Event, &entry.Outcome, &entry.Content, &entry.ContentURL); err != nil {
			rows.Close()
			return page, err
		}
		page.Entries = append(page.Entries, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	return page, tx.Commit()
}
