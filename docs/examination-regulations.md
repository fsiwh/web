# Prüfungsordnungen

## UI and data

- One row per existing Studiengang × Abschluss × PO label (`program_versions`).
- Compact list with search, Studiengang/Abschluss filters and pagination.
- Direct PO and Modulhandbuch PDF links. The PO label opens a read-only detail
  page with document dates, amendments, the Rahmenprüfungsordnung and sources.
- Keep the archive's PO labels unchanged. A missing document mapping is shown
  explicitly; do not guess from the year or another degree.
- References are curated in `model/exams/regulation_sources.go`. Reviewed PDFs
  are imported into configured file storage with `-import-regulations DIRECTORY`.
  The database records original URL, filename, SHA-256, size and import time.
  Imported files are served through administrator-only routes; references fall
  back to WHS originals before import. No PDFs are committed to the repository.
  Repeat imports verify existing snapshots and reject changed source bytes.
  The application does not fetch PDFs during page rendering.

## Research, 25 October 2026

The WHS publishes study-program POs, separate degree-wide Rahmenprüfungsordnungen,
amendments and independently updated module handbooks. A publication year or an
updated MHB date is not automatically a new PO label. Shared Amtsblatt PDFs can
contain changes to several different POs.

| Studiengang | Abschluss | WHS grouping | PO document date | MHB cover date |
| --- | --- | --- | --- | --- |
| Informatik | Bachelor | 2023 | 03.05.2023 | 09.10.2026 |
| Informatik | Master | 2023 | 03.05.2023 | 22.09.2026 |
| Wirtschaftsinformatik | Bachelor | 2023 | 03.05.2023 | 09.10.2026 |
| Wirtschaftsinformatik | Master | 2023 | 03.05.2023 | 22.09.2026 |
| Internet-Sicherheit | Master | 2023 | 03.05.2023 | 22.09.2026 |
| Medieninformatik | Master | 2023 | 03.05.2023 | 22.09.2026 |
| Informatik und Design | Bachelor | 2023 | **08.01.2025** | 22.09.2026 |
| Medieninformatik | Bachelor | 2016 | Not stated on the linked PDF cover | 17.09.2025 |

Important source details:

- Informatik und Design is listed on the WHS “2023” page and its MHB says PO2023,
  but the actual PO is dated 08.01.2025, published 17.06.2025. The detail page
  explains this mismatch rather than silently relabelling the archive.
- Medieninformatik Bachelor has an Auslaufordnung: module examinations run until
  30.04.2027; the programme is abolished on 31.08.2027. Link the official document
  alongside its PO and MHB, retaining the full conditions in the original.
- Existing archive labels such as 2017 are not silently mapped to PO2016.
- The downloaded PO/MHB files and linked amendment PDFs were checked for PDF
  content; PO/MHB cover text and the Auslaufordnung were inspected. URLs can be
  replaced in place by the WHS, so the recorded check date is not a guarantee of
  future content or a claim that a base PO PDF consolidates all amendments.

## Official sources

- [Bachelor POs and amendments](https://www.w-hs.de/informatik/info-center/studium-2023/bachelor-pos/)
- [Master POs and amendments](https://www.w-hs.de/informatik/info-center/studium-2023/master-pos/)
- [Bachelor module handbooks](https://www.w-hs.de/informatik/info-center/studium-2023/studienverlauf-bachelor/)
- [Master module handbooks](https://www.w-hs.de/informatik/info-center/studium-2023/studienverlauf-master/)
- [Bachelor Rahmenprüfungsordnung and amendments](https://www.w-hs.de/informatik/info-center/studium-2023/bachelor-rpo/)
- [Master Rahmenprüfungsordnung and amendments](https://www.w-hs.de/informatik/info-center/studium-2023/master/rpo/)
- [Medieninformatik Bachelor PO2016 and Auslaufordnung](https://www.w-hs.de/informatik/info-center/studium-2016/bpo/)
- [Medieninformatik Bachelor module handbook](https://www.w-hs.de/informatik/info-center/studium-2016/studienverlauf-bachelor/)

Direct PDF URLs and the programme-specific amendment mappings are recorded in
the source catalogue, keeping legal documents distinct from their landing pages.
