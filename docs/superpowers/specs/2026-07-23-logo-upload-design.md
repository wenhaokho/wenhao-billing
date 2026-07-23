# Logo Upload in Account Settings — Design

**Date:** 2026-07-23
**Status:** Approved
**Scope:** Replace the "Logo URL" text input in the Company/business-profile
settings with a file-upload control and preview. **Upload + preview only** — no
invoice-PDF rendering in this change.

## Problem

The Company form (`AccountView.vue`) exposes the business logo as a free-text
`Logo URL` field, backed by the `business_profile.logo_url` `Text` column. The
value is currently rendered nowhere (neither the invoice PDF nor any other
view uses it), and asking the user to host an image somewhere and paste a URL is
poor UX. The user wants to upload an image file directly.

## Approach

Store the uploaded image **in the database as a base64 data URI** in the
existing `logo_url` column. For a single-tenant app with a singleton
business-profile row, this is the minimal robust option:

- No new column, no Alembic migration — a data URI is still valid `Text`.
- No disk volume, no `StaticFiles` mount, no new upload endpoint — the existing
  `PUT /business-profile` already accepts `logo_url` as free text.
- Renders directly in an `<img src="data:…">` preview.
- Survives container restarts (lives in Postgres).
- If invoice-PDF rendering is added later, reportlab can consume the bytes
  straight from the decoded data URI.

**Rejected alternative:** multipart upload endpoint + mounted media volume +
`StaticFiles` serving. More appropriate for large or numerous assets, but it is
real infrastructure for a single admin logo. Revisit only if per-customer logos
or large assets appear (YAGNI).

## Components

### Backend
No changes. The existing endpoints already cover this:

- `GET /business-profile` returns `logo_url` (now possibly a data URI).
- `PUT /business-profile` persists `logo_url` unchanged.

### Frontend — `frontend/src/views/AccountView.vue`
Replace the `Logo URL` `InputText` block with:

1. **File picker** — PrimeVue `FileUpload` in custom/basic mode (choose-file
   only, `:auto="false"`, no server upload). On file select, run client-side
   validation, then convert via `FileReader.readAsDataURL` and assign the
   resulting data URI to `company.logo_url`.
2. **Preview** — a thumbnail `<img :src="company.logo_url">` shown whenever
   `company.logo_url` is non-empty (bounded max-height, e.g. 64px).
3. **Remove** — a button that sets `company.logo_url = ""`.
4. **Save** — unchanged; the existing submit handler PUTs the profile including
   the new `logo_url`.

## Validation & errors

Client-side guard on file select:

- **Type:** allow `image/png`, `image/jpeg`, `image/svg+xml`. Reject others.
- **Size:** reject files larger than **1 MB** (keeps the base64 payload small,
  since `logo_url` is returned on every profile fetch).

On rejection, surface an inline error using the existing `companyError` /
`Message` pattern already present in the form. Do not assign an invalid file to
`company.logo_url`.

## Data flow

```
user selects file
  → validate type + size (reject → inline error, stop)
  → FileReader.readAsDataURL → data URI
  → company.logo_url = dataURI  (preview updates reactively)
user clicks Save
  → PUT /business-profile { …, logo_url: dataURI }
  → persisted in business_profile.logo_url (Text)
GET /business-profile
  → logo_url data URI → <img> preview renders
```

## Out of scope

- Invoice-PDF logo rendering (`invoice_pdf.py` untouched).
- Backend upload endpoint / multipart handling.
- New DB column or migration.
- Image downscaling/optimization (the 1 MB cap is the only size control).
- SVG sanitization beyond `<img>`-tag rendering (safe: `<img>` does not execute
  embedded SVG scripts).

## Testing

- Manual: upload PNG → preview appears → Save → reload page → preview persists.
- Manual: oversize file (>1 MB) and wrong type (e.g. `.pdf`) → inline error,
  field unchanged.
- Manual: Remove → preview clears → Save → reload → logo gone.
