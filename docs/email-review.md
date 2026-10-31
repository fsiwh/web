# Email template review

## Local review

Generate examples from the actual public `Sender` methods and shared template:

```sh
preview_root="${TMPDIR:-/tmp}"
preview_dir="$(mktemp -d "${preview_root%/}/fsi-email-previews.XXXXXX")"
nix develop -c go test ./internal/email -run '^TestEmailPreviews$' -count=1 \
  -args -email-preview-dir "$preview_dir"
python3 -m http.server 8094 --bind 127.0.0.1 --directory "$preview_dir"
```

Open <http://127.0.0.1:8094>. The gallery has desktop/mobile views, decoded HTML
and text alternatives, and downloadable `.eml` files for opening in a mail
client. The `.eml` files retain the subject, sender, recipient and reply-to headers.

Re-run the generation command after editing email copy or the template, then
refresh the browser. Keep `preview_dir` in the same shell to update the existing
gallery. The preview uses an isolated, migrated fixture database and captures
SMTP on loopback. It does not read SMTP credentials or send external mail.
Organization branding is representative fixture data; addresses, recipients,
feedback and links are synthetic. No production accounts or tokens are used.

Examples cover registration, email-change confirmation, invitation, password
reset, password-change notice, email-change notice to the previous address,
reviewer notification, approval, rejection, and all three website-feedback cases.
Registration and email-change confirmation deliberately show the same current
message: that is a finding, not a preview substitution.

The browser preview checks layout; the `.eml` download also allows review in
Apple Mail, Outlook or other mail clients, whose HTML rendering can differ.

## GOV.UK guidance reviewed

Reviewed on 31 October 2026:

- [Planning and writing text messages and emails](https://www.gov.uk/service-manual/design/sending-emails-and-text-messages):
  one purpose per message, important information first, clear actions and
  deadlines, recognisable sender, personalisation and contact information.
- [Notify: formatting emails and letters](https://www.notifications.service.gov.uk/using-notify/formatting):
  simple typography, sentence-case headings and minimal formatting.
- [Notify: links and URLs](https://www.notifications.service.gov.uk/using-notify/links-and-urls):
  recognisable full URLs; descriptive link text is also supported for long or
  complex URLs. Avoid vague labels and third-party shorteners.
- [Design System: confirm an email address](https://design-system.service.gov.uk/patterns/confirm-an-email-address/):
  clear confirmation journey, expiry, replacement and single use of links.
- [Notify: fonts and typefaces](https://www.notifications.service.gov.uk/using-notify/fonts-typefaces):
  Helvetica, Arial and device sans-serif fallback; 19 px email body text.
- [Notify: email branding](https://www.notifications.service.gov.uk/using-notify/email-branding)
  and [reply-to address](https://www.notifications.service.gov.uk/using-notify/reply-to-email-address):
  recognizable organization identity and a usable reply route.
- [Notify's actual HTML email template](https://github.com/alphagov/notifications-utils/blob/main/notifications_utils/jinja_templates/email_template.jinja2):
  the message ends in its content column, without a separate institutional footer.
- [Service Manual: sending emails](https://www.gov.uk/service-manual/technology/how-to-email-your-users):
  accessible content, reply handling and testing in real email clients.
- Design System components and styles:
  [headings](https://design-system.service.gov.uk/styles/headings/),
  [links](https://design-system.service.gov.uk/styles/links/),
  [buttons](https://design-system.service.gov.uk/components/button/),
  [inset text](https://design-system.service.gov.uk/components/inset-text/),
  [warning text](https://design-system.service.gov.uk/components/warning-text/),
  [panels](https://design-system.service.gov.uk/components/panel/) and
  [notification banners](https://design-system.service.gov.uk/components/notification-banner/).

These are email-specific recommendations, rather than a requirement to embed
the website's GOV.UK Frontend styles in email. GOV.UK-specific branding and domain
requirements do not apply to the Fachschaft; use its own identity and domain.

## Current implementation

`internal/email/templates/notification.html` is the single shared HTML template.
`internal/email/email.go` supplies message-specific content and produces a
`multipart/alternative` message with UTF-8 plain text and HTML. Both versions use
the same notification data. The preview captures this actual MIME output.

Already useful and consistent with the guidance:

- Recognisable organization header and sender display name, named greeting,
  contact link and configurable reply-to address.
- One main heading and action per message; readable Arial/Helvetica text.
- Full destination URLs, no tracking pixels, remote images or attachments.
- Expiry information on confirmation, invitation and password-reset emails.
- Escaped user content, plain-text alternative and presentation-only tables.

## Findings and recommendations

### 1. Email-change confirmation needs contextual copy

`SendVerificationEmail` distinguishes invitations, but not account email changes.
Originally the new address received registration-specific wording, including
“Wenn du dich nicht registriert hast”. The local revision uses neutral wording
that also makes sense for an address change. A separate email-change message
would still explain the effect on the existing address more clearly.

Use separate copy for the new-address confirmation, for example:

- Subject: **Neue E-Mail-Adresse bestätigen**
- Intro: **Bestätige deine neue E-Mail-Adresse für dein Benutzerkonto. Bis dahin
  bleibt deine bisherige Adresse gültig.**
- Action: **Neue E-Mail-Adresse bestätigen**
- Expiry: **Der Link ist 24 Stunden gültig.**
- Note: **Wenn du diese Änderung nicht angefordert hast, kannst du diese E-Mail
  ignorieren. Deine bisherige Adresse bleibt gültig.**

Keep the subsequent notice to the previous address separate. This change will
need the mail queue to retain the verification purpose through delivery.

### 2. Learning-material terminology and direct language (revised locally)

Reviewer and decision messages used “Altklausuren” and “Einsendung”. The local
revision uses “Lernmaterial” and “Einreichung” consistently, and shorter copy:

- Reviewer subject: **Neue Einreichung zur Prüfung**
- Reviewer intro: **Neues Lernmaterial wartet auf deine Prüfung.**
- Approved intro: **Deine Einreichung wurde freigegeben. Vielen Dank für deinen Beitrag!**
- Rejected intro: **Deine Einreichung wurde abgelehnt. Bei Fragen wende dich an die Fachschaft.**

The reviewer subscription note now names the current settings page. Link to the working
`/exams/settings` preference directly; the account notification page is currently
UI-only and cannot disable that subscription.

### 3. Empty optional paragraphs (revised locally)

The HTML template now omits empty `Validity` and `Note` paragraphs. The text
version can still be simplified to avoid extra blank lines. Review password-change
and feedback emails in particular.

### 4. Website-feedback line breaks (revised locally)

The HTML version now preserves line breaks in introductions, problem descriptions
and notes, while still escaping all submitted content. “Seite” and “Rückmeldung”
appear on separate lines. The text alternative also retains the line breaks.
Review the problem-report example to see multi-line input.

### 5. Long action links dominate the layout

Full URLs are consistent with GOV.UK guidance and make the destination visible.
Keep them readable and wrapping at narrow widths. Notify also permits descriptive
link text for complex URLs, so an action-labelled link is an option to review,
not a reason to introduce a website-style button. Retain the full URL in text mail.

## Component-by-component review

| Element | Current email | Assessment and recommendation |
| --- | --- | --- |
| Header / brand | Blue text header with the organization name and a home link | Recognizable identity is useful. Retain the Fachschaft's identity here, rather than repeating its abbreviation in each instruction. No government crest or GOV.UK wordmark is needed. |
| Subject and main heading | The subject is repeated as one semantic `h1`, 36 px desktop / 28 px mobile | Clear sentence-case headings are appropriate. This is email-specific sizing, not a claim that the website type scale must be copied. Keep one heading; avoid an additional large status panel. |
| Greeting | `Hallo Alex Müller,` | Personalized and consistent with the site's German “du” voice. GOV.UK's English example uses “Dear”, but that is not a requirement to translate the greeting into formal German. Keep the name and avoid repeating the brand in the greeting. |
| Paragraphs | 19 px Helvetica/Arial, line-height 1.5, left aligned | Matches Notify's body font guidance. Keep body text at 19 px on mobile too. No webfont dependency or lead paragraph is needed. |
| Main action | Action label followed by an underlined full URL | A real link is appropriate for opening the service. Keep one main action, with wording that says what happens next. Notify permits descriptive link text for complex URLs. Do not insert a form button, JavaScript behavior or copied `govuk-button` markup into the email. |
| Link styling | Blue underlined body link; header link has no underline | Body links are identifiable beyond color. A brand link in a recognizable header can omit its underline. Long URLs wrap, but `word-break: break-all` can split words unnecessarily; apply aggressive wrapping only to the URL, not the action label. |
| Expiry notice | Ordinary paragraph under the action | Good placement. Keep it as plain text beside the action. An inset or warning icon is unnecessary for a routine one-hour or 24-hour expiry. Render it only when present. |
| Security instructions | Paragraph after the action | Keep conditional instructions clear: what to do **if you did not request the change**. The password-change notice currently presents “Passwort zurücksetzen” as the main action for everyone; make that recovery link conditional in the wording so a successful change does not appear to require another reset. |
| Inset text | Not used | No need to add it to every message. Notify supports inset text sparingly; critical recovery instructions should remain prominent main content, not an aside that can be missed. |
| Warning text | No warning icon or colored box | No missing component here. Web warning text is for important consequences, not every routine account email. Use clear conditional security text rather than adding an alarm icon to all notices. |
| Panels / notification banners / tags | Not used | Appropriate. The email itself communicates the outcome. Web confirmation panels, success banners and status tags would duplicate the heading and introduce extra visual weight. Convey approval/rejection in words, not color alone. |
| Layout | Single 640 px maximum-width table; white content on a gray background; 40/30 px desktop and 30/20 px mobile padding | Sensible email layout. Content is left aligned within a centered wrapper. Do not copy the website's 1100 px container, sidebar or two-thirds grid into email. Empty optional HTML paragraphs have been removed locally. |
| Footer / sign-off | “Deine Fachschaft” and a contact link in the body column (revised locally) | Follows Notify's simple message ending: no separate gray footer, dividing border or repeated organization/institution line. The contact link uses the same 19 px type and blue underline as the body links. |
| Plain text | Same content, complete links, UTF-8 MIME alternative | Important fallback. Remove empty sections alongside the HTML cleanup, and preserve feedback line breaks in both versions. |
| Website feedback | Uses the same greeting/action/validity/note slots | This is an internal report, not an account action. Use labeled content sections such as “Seite”, “Rückmeldung”, “Was wolltest du tun?” and “Was ist passiert?”; do not treat report content as an expiry notice. Preserve multi-line input. |

### Recommended reading order

1. Organization identity.
2. A short heading explaining the purpose or outcome.
3. Named greeting.
4. One or two short sentences with the important information first.
5. A clear action link, if action is needed.
6. Expiry information, if applicable.
7. Conditional security/help text, if needed.
8. Short sign-off and contact link within the message column.

Avoid adding decorative components to fill these roles. The website's component
guidance helps decide which information needs emphasis; Notify's supported email
elements and real mail-client behavior determine the implementation.

### Local copy revisions

The local gallery now includes these edits:

- Registration/confirmation: “Bestätige deine E-Mail-Adresse, um dein Benutzerkonto zu nutzen.”
- Neutral unexpected-email note: “Wenn du diese E-Mail nicht angefordert hast, kannst du sie ignorieren.”
- Invitation: “Einladung zur Fachschaft”, with short password and address-confirmation instructions.
- Password reset: “Du kannst ein neues Passwort für dein Benutzerkonto festlegen.”
- Email-change notice: shorter explanation that the notice goes to the previous address.
- Reviewer and submission decisions: current terminology, direct action labels and shorter paragraphs.
- Sign-off: “Deine Fachschaft” instead of repeating the organization abbreviation.

Organization names remain in the sender/header to identify who sent the
message. The request to simplify branding applies to task instructions, not to
removing sender identity.

## Verification and scope

All 12 current examples were checked in Chromium and WebKit at 1365 px and 390 px:
one main heading per email and no horizontal overflow. Exported messages use
the actual MIME output, including text alternatives and real-length dummy tokens.
These browser checks do not establish rendering in Outlook or Gmail; use the
downloadable `.eml` files for mail-client review.

The copy, template and preview-tooling changes do not alter account flows or
subscription behavior. Layout/component recommendations above remain review
findings unless explicitly marked revised.
