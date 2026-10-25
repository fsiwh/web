# Access

Current permissions for active, verified accounts. Roles apply across the site;
access is not scoped to an organisation.

**Yes** = allowed. **Own** = only records belonging to the signed-in account.
**No** = not allowed. Ownership and workflow checks still apply on the server.

## Access matrix

| Area / action | User (`user`) | Editor (`editor`) | Admin (`admin`) |
| --- | --- | --- | --- |
| Read public pages, discussions, news, events and public photos | Yes | Yes | Yes |
| Browse and download published Altklausuren | Yes | Yes | Yes |
| Change own Altklausuren search preferences | Own | Own | Own |
| Upload and submit Altklausuren | Yes | Yes | Yes |
| View own submissions and drafts | Own | Own | Own |
| Edit own submissions in an editable workflow state | Own | Own | Own |
| Submit a revision of an approved exam document | Own | Yes | Yes |
| View submitted work in Verwaltung, excluding drafts | No | Yes | Yes |
| Review, approve, reject or edit submitted work | No | Yes | Yes |
| Change own submission-notification preference | No | Own | Own |
| Create discussions, comment and vote | Yes | Yes | Yes |
| Edit discussions | Own | Yes | Yes |
| Edit comments | Own | Yes | Yes |
| Create and edit news | No | Yes | Yes |
| Create and edit events and their cover images | No | Yes | Yes |
| Manage photo visibility on the homepage | No | Yes | Yes |
| Open Verwaltung | No | Yes | Yes |
| List modules in Verwaltung | No | Yes | Yes |
| List Studiengänge in Verwaltung | No | Yes | Yes |
| List Prüfungsordnungen and inspect/download document references | No | Yes | Yes |
| Edit contact details and public links | No | Yes | Yes |
| Read organisation identity and legal settings | No | Yes | Yes |
| Edit organisation identity and legal settings | No | No | Yes |
| Edit existing public team names and organisational functions/assignments | No | Yes | Yes |
| Invite/link members and resend invitations | No | No | Yes |
| Create, edit and publish meeting invitations and minutes | No | Own | Own |
| List and inspect Benutzerkonten | No | No | Yes |
| Send password-reset or account-confirmation emails | No | No | Yes |
| View and search editorial Aktivitäten | No | Yes | Yes (full history) |
| View and search account/security Aktivitäten | No | No | Yes |

Public pages are also available without signing in. Altklausuren require sign-in.
Private profiles and unpublished photos are not made public by a higher role.
The module and study-program management pages currently provide lists only.
Prüfungsordnungen also have read-only document detail pages.

## Editor / administrator boundary

Editors manage content; administrators additionally manage accounts, invitations
and protected organisation settings. Organisational functions are public team
assignments, not login access roles.

- Editing a team name stores a public display-name override. Login identity,
  account email, account linkage and access role are not changed. Editors do not
  receive account details or invitation controls in team management.
- Editors can read identity/legal settings but can save only contact details and
  public links. The organisation model rejects protected-field changes as well
  as the route policy rejecting writes to protected forms.
- Both roles retain meeting-document ownership restrictions: another person's
  private draft cannot be edited or published. Published files remain immutable.
- Editors' Aktivitäten show only an explicit allowlist of editorial events.
  Account/security events, private submission drafts and unknown future event
  types are excluded before pagination, counting, filter options and search.
  The query does not join accounts; names, emails, account identifiers and links
  are not exposed or searchable. Public content titles and public team names may
  still identify people; this is a content-focused view, not an anonymity claim.
- Successful news/discussion saves, comment edits, event saves, photo-visibility
  changes, meeting publications, public team/function edits and editorial settings
  saves are recorded. Submitted exam work is included only while accessible to
  reviewers. Administrators retain the full audit history and actor attribution.

## How access is enforced

- **RBAC:** `user` has normal signed-in access; `editor` adds editorial and
  submission-review access; `admin` adds administration.
- **Record-level access:** ownership limits access to drafts, personal
  submissions and comments. Reviewers cannot inspect another account's drafts.
- Route middleware checks sign-in and required roles. Model services enforce
  ownership, workflow and additional role checks, including on routes shared by
  all signed-in accounts. Hiding a link is not an access check.
- Inactive, unverified or deleted accounts cannot keep an authenticated session.
  Gaining editor/admin privileges requires a fresh sign-in. See
  [session policy](session.md) for session limits.

## Code references

- [Route policies](../site/routes.go) and [middleware](../site/middleware.go)
- [Session roles](../model/users/service.go)
- [Submissions](../model/exams/submissions.go) and [exam revisions](../model/exams/upload.go)
- [Discussions and comments](../model/posts/service.go)
- [News publishing](../model/posts/draft.go)
- [Event publishing](../model/events/cover.go)
- [Photo visibility](../model/media/visibility.go)
- [Public team names](../model/members/public.go)
- [Organisation field permissions](../model/organization/organization.go)
- [Editorial activity projection](../internal/audit/editorial.go)
