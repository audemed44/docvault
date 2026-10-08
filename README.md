# Docvault

Self-hosted home for scanned family documents: iPhone Shortcut uploads, OCR search, private and family libraries.

A CamScanner replacement for a family on one homelab. Scan on the iPhone,
**Share → Save to Vault**, and the PDF lands on your server. There it gets a
thumbnail, its text is pulled out (Tesseract OCR when it's a scan without a
text layer, in English and Hindi), and it becomes searchable. One small Go
binary with the web UI built in, data in SQLite, files on disk.

- **Accounts**: each person has a private library. A shared **Family**
  space holds what everyone should see. Admins add people; there's no
  sign-up.
- **Uploads**: from the iOS Shortcut (personal API token), the web UI
  (drag and drop, several files), a whole folder in the browser, or a
  folder on the server (`/data/import/<username>/`). PDF, JPEG, PNG and
  HEIC; photos are shown as a PDF, and the original is kept too.
- **Originals stay byte for byte** in `/data/files/<id>/`. Thumbnails and
  photo PDFs in `/data/cache/` can be rebuilt, and so can the text.
- **Organising**: one category, any tags, a document date, notes, and an
  expiry date (passport, policy, licence) that shows up as "expiring soon".
- **Search**: SQLite FTS5 over titles, notes, tags and the text, with
  filters for space, category, tag and year.
- **Duplicates** (same contents, same space) are skipped, so imports can
  run again.
- **Suggestions (optional)**: a classifier service suggests a category,
  tags and dates; see below.

## Run it

```yaml
services:
  docvault:
    image: ghcr.io/audemed44/docvault:latest
    restart: unless-stopped
    user: "1000:1000"
    environment:
      - DOCVAULT_TOKEN=${DOCVAULT_TOKEN} # openssl rand -hex 32
    volumes:
      - ./docvault:/data
    ports:
      - "8090:8080"
    mem_limit: 768m # idles at ~25 MB; OCR peaks ~460 MB per page, briefly
```

Then open it and create the first account (an admin) with the
`DOCVAULT_TOKEN`. See [docker-compose.example.yml](docker-compose.example.yml)
for every option.

| Variable | Default | |
|---|---|---|
| `DOCVAULT_TOKEN` | (required) | Creates the first account; Foyer's widget key |
| `DOCVAULT_DATA_DIR` | `/data` | Database, files, cache and import folders |
| `DOCVAULT_PORT` | `8080` | Port inside the container |
| `HOMEPAGE_URL` | | Foyer's address, linked from the header |
| `DOCVAULT_CLASSIFIER_URL` | | A service that suggests categories and tags (below) |
| `DOCVAULT_CLASSIFIER_TOKEN` | | Sent to it as a bearer token |
| `DOCVAULT_DEBUG` | | Set to log debug messages |

Uploads can be big (multi-page scans are 10–30 MB): allow at least 100 MB
request bodies in the reverse proxy.

## The iPhone Shortcut

Make a token under **Settings → iPhone**, then build a Shortcut (or share
one and put its iCloud link in Settings → Processing):

```
Receive  PDFs, Images  from  Share Sheet
Get Contents of URL  https://docs.example.com/api/categories?format=names
   Headers: Authorization: Bearer <token>
Choose from List  (Contents of URL)
Ask for Input  "Title?"  (default: Current Date)
Get Contents of URL
   POST  https://docs.example.com/api/upload
   Headers:  Authorization: Bearer <token>
   Body (Form):  file = Shortcut Input
                 category = Chosen Item
                 title = Provided Input
If  Contents of URL  contains "ok"  → Show Notification "Saved ✓"
Otherwise                           → Show Alert (Contents of URL)
```

`POST /api/upload` answers in plain text (`ok: saved “Title”`,
`ok: already saved as “…”`, or `error: …`) unless the request asks for
JSON. Other fields: `tags` (comma-separated), `date` (YYYY-MM-DD), `notes`,
`space` (`family` or `private`). Several files in one request become
separate documents. API tokens can upload and read but can't change
account settings.

## Suggestions: the classifier hook

When `DOCVAULT_CLASSIFIER_URL` is set, Docvault POSTs each document after
its text is read:

```json
{
  "document_id": 42,
  "title": "Scan 2026-10-08",
  "text": "SAMPLE GENERAL INSURANCE … (up to 24 KB)",
  "categories": ["ID", "Property", "Medical", "Insurance", "Tax", "…"],
  "tags": ["car", "policy", "…tags already in use"]
}
```

and expects (every field optional):

```json
{ "title": "Car insurance policy", "category": "Insurance", "tags": ["car"],
  "doc_date": "2025-11-20", "expires": "2026-11-19" }
```

Unknown categories and malformed dates are dropped. The suggestion is shown
on the document with **Apply** and **Dismiss**; nothing changes until
someone applies it. Whatever the URL points at receives the documents'
text, so point it at something on your own server (a small wrapper around
a local model) to keep documents private.

## Foyer

Docvault serves a [Foyer](https://github.com/audemed44/foyer) card at
`/api/foyer/widget`: counts, then documents expiring soon (or the newest)
as covers, from the first admin's library and the Family space. Files
dropped in Foyer's Drop go to that admin's Inbox.

```yaml
      - name: Docvault
        url: https://docvault.example.com
        container: docvault
        widget:
          type: app
          url: http://docvault:8080/api/foyer/widget
          key: ${DOCVAULT_TOKEN}
```

## Development

Processing needs poppler (`pdfinfo`, `pdftoppm`, `pdftotext`), `tesseract`
(with `eng` and `hin`) and `heif-dec`; without them documents fail with a
clear error, and their tests are skipped.

```sh
cd frontend && npm install && npm run build && cd ..
DOCVAULT_TOKEN=dev DOCVAULT_DATA_DIR=./data go run ./cmd/docvault
# or, with hot reload: run the binary, then `npm run dev` in frontend/
```
