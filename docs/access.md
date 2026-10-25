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
| Edit comments | Own | Own | Yes |
| Create and edit news | No | Yes | Yes |
| Create and edit events and their cover images | No | Yes | Yes |
| Manage photo visibility on the homepage | No | Yes | Yes |
| Open Verwaltung | No | Yes | Yes |
| List modules in Verwaltung | No | No | Yes |
| Manage Stammdaten | No | No | Yes |
| Manage members, invitations and organisational functions | No | No | Yes |
| Manage meeting invitations and minutes | No | No | Yes |
| List and inspect Benutzerkonten | No | No | Yes |
| Send password-reset or account-confirmation emails | No | No | Yes |
| View and search Aktivitäten | No | No | Yes |

Public pages are also available without signing in. Altklausuren require sign-in.
Private profiles and unpublished photos are not made public by a higher role.
The module-management page currently provides the list only.

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
