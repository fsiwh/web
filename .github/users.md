# Users and functions

Every person is a login account in `users`. Access is a single global role:
`user`, `editor`, or `admin`. Organizational functions are independent assignments:

```text
users --< user_functions >-- functions >-- organizations
```

- `organizations`: ID and name. Existing functions belong to Fachschaft Informatik.
- `functions`: organization, kind (`board`, `departments`, `project`), title,
  shared public email, and display order.
- `user_functions`: user, function, and display order within the function.
- `users.public_email`: optional public contact address; login email stays private.

An account may hold several functions; a function may have several accounts.
An editor invitation requires at least one existing function. An admin invitation
does not require assignments. Account creation/promotion, assignments, and queued
mail commit together. Existing passwords, verification, activation, and stronger
access roles are preserved when inviting an existing account.

Invitations start with access type. Organizational access proceeds through
function kind, function checkboxes, and a list of added assignments. FSI is selected
implicitly; organization selection and labels are omitted from the invitation UI.
The list supports adding another group or removing assignments. System
administration skips directly to the invitee's name and email. No account or mail
is created until the final “Einladung senden” submission. The flow works without
JavaScript and uses the GOV.UK question-page and departmental add-to-a-list patterns:

- https://design-system.service.gov.uk/patterns/question-pages/
- https://design.homeoffice.gov.uk/design-system/patterns/help-users-to/add-multiple-things

`invitation_pending` explicitly distinguishes password-setup invitations from
self-registration verification. Invitations no longer depend on a profile row.
All user addresses are stored lowercase and trimmed, with case-insensitive
database uniqueness.

Account administration is under `/admin/users`; function administration remains
under `/admin/roles`. The `model/members` store is a team/function projection of
users, not a second identity store. The public team page shows assignments for
Fachschaft Informatik, using function mailboxes before public account addresses.
Current application access roles remain global; organization assignments describe
positions and do not by themselves add organization-scoped content permissions.

## Migration

Migration 31 removes `people`, `people_state`, and `responsibility_members`, renames
`responsibilities` to `functions`, and copies memberships into `user_functions`.
Existing linked accounts keep their IDs and credentials. Unlinked profiles reuse
an account with the same normalized email, or become unverified editor accounts
awaiting an invitation/password setup. No invitation emails are sent by migration.
Public addresses, shared function mailboxes, assignment ordering, function IDs,
and legal-contact references are retained. Old member public IDs stay permanently
reserved and retired; account URLs use their existing user public IDs.

Profiles without a usable account email, multiple profiles resolving to one
account, or case-only account email collisions stop the migration with an explicit
error. Resolve these source records before retrying; the identity migration is
transactional and does not invent addresses or discard profiles.
