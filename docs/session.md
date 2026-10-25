# Sessions

## Policy

| | User | Editor | Administrator |
| --- | --- | --- | --- |
| Default maximum lifetime | 8 hours | 8 hours | 8 hours |
| Inactivity timeout | 30 minutes | 30 minutes | 30 minutes |
| With “Angemeldet bleiben” | 14 days, no inactivity timeout | No extension | No extension |
| Warning before expiry | 2 minutes | 2 minutes | 2 minutes |

| Event | What happens |
| --- | --- |
| Activity | Extends the inactivity deadline, never the maximum lifetime |
| Logout or password reset | Invalidates the session |
| Account disabled or deleted | Access ends when the session is next checked |
| User gains administrative/editorial rights | Existing session is invalidated; signing in again is required |
| Editor becomes administrator | Existing session is invalidated; signing in again is required |

## Timeout warning

| Situation | Behaviour |
| --- | --- |
| Two minutes before expiry | Show a warning dialog with an accessible countdown |
| Choose “Angemeldet bleiben” or press Escape | Extend the inactivity deadline and keep the current page |
| Maximum lifetime reached | Require a new sign-in |
| No response before expiry | Show an expiry page; saved content is kept, unsaved input may be lost |
| Other tabs are active | Use the shared server-side deadline |
| Background status check | Does not extend the session |
| JavaScript unavailable | Server still enforces expiry |

The dialog supports keyboard navigation and returns focus when closed.

## Why

Thirty minutes limits unattended access while allowing time to read and edit.
The optional 14-day login makes ordinary use more convenient without giving
privileged accounts long-lived access. These durations are our risk-based choice,
not a universal requirement from the sources below.

Session cookies use HTTPS, HttpOnly and SameSite=Lax. Only hashes of random
session tokens are stored in SQLite, so sessions survive application restarts.

## Sources

- [OWASP: Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html#session-expiration): separate inactivity and maximum lifetimes; enforce them on the server.
- [BSI: CON.10, Edition 2023](https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Grundschutz/IT-GS-Kompendium_Einzel_PDFs_2023/03_CON_Konzepte_und_Vorgehensweisen/CON_10_Entwicklung_von_Webanwendungen_Edition_2023.pdf?__blob=publicationFile&v=3): A3 covers session expiry; A12 covers renewed authentication for important changes.
- [NIST: SP 800-63B-4](https://pages.nist.gov/800-63-4/sp800-63b/session/): overall and inactivity timeouts; caution with persistent bearer cookies.
- [GOV.UK DWP: Session timeout](https://design-system.dwp.gov.uk/patterns/manage-a-session-timeout/how-it-works): warning at least two minutes before expiry; extension with Escape.
- [GOV.UK Home Office: Timing out](https://design.homeoffice.gov.uk/design-system/patterns/help-users-to/manage-service-timing-out): focus handling, accessible countdowns and a clear expiry page.
