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
- **Suggestions (optional)**: a chat model (OpenRouter, or a local one)
  suggests a title, category, tags and dates from the text, which is
  **masked first**; see below.

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
| `HOMEPAGE_URL` | | Foyer's address, linked from the header (admins only) |
| `DOCVAULT_LLM_KEY` | | API key for suggestions from a chat model (below) |
| `DOCVAULT_LLM_MODEL` | | The model, e.g. an OpenRouter model ID |
| `DOCVAULT_LLM_URL` | `https://openrouter.ai/api/v1` | Any OpenAI-compatible API (llama.cpp, Ollama…) |
| `DOCVAULT_CLASSIFIER_URL` | | Or: your own service that takes the input as JSON (below) |
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

## Suggestions

With `DOCVAULT_LLM_KEY` and `DOCVAULT_LLM_MODEL` set, documents can be
sent to a chat model, whose answer shows on the document as a suggestion:
a title, a category, tags and dates, with **Apply** and **Dismiss**.
Nothing changes until someone applies it.

- **When:** by default only when someone asks: **Get a suggestion** on a
  document, or **Select** documents in the library and **Get
  suggestions**. Settings → Suggestions → When can make it automatic for
  every new document instead.
- The library shows how many suggestions are waiting, to **Review** and
  **Apply all**, or apply just the selected ones.

- **Often just the name is sent.** By default (Settings → Suggestions →
  Send: "auto"), a document whose name says what it is ("Dad passport
  2019") is classified from its name alone; scanner names ("CamScanner
  03-15-2021 10.22", "IMG_2041") send the text too, and so does a name
  that wasn't enough to pick a category. It can also be "only the name"
  or "the name and the text" every time.
- **Only masked text is sent**, at most 12 KB of it, never the file.
  `internal/mask` replaces Aadhaar and VID numbers, PAN, passport, driving
  licence and vehicle numbers, card numbers (Luhn) and Aadhaar (Verhoeff)
  by checksum, phones, emails, any other run of 6+ digits, labelled names,
  addresses and birth dates ("Name:", "S/O", "Address:", "DOB:"), the
  family's names (Settings → Suggestions: they become `[person:1]`…, so
  no names leave the server) and any other words you list. Dates and
  amounts are kept. It's a strong reduction, not anonymisation:
  unlabelled names and addresses, and what a document is about, still go
  through. **What the classifier saw** on each document shows exactly what
  was sent, and Settings has a box to try the masking on any text.
- **Only your categories and tags** can come back: the request carries a
  JSON schema listing them, and anything else is dropped (Settings → Tags
  manages the list; people are tags too).
- On OpenRouter, requests ask for providers that neither keep nor train
  on prompts (`provider: {data_collection: "deny", zdr: true}`). A local
  model (`DOCVAULT_LLM_URL=http://llama:8080/v1`) keeps everything on
  the server.
- Asking again reuses a document's OCR text instead of running OCR again.

### Your own classifier

`DOCVAULT_CLASSIFIER_URL` instead POSTs the same masked input as JSON:

```json
{
  "document_id": 42,
  "title": "CamScanner 03-15-2021 10.22",
  "text": "INCOME TAX DEPARTMENT … [pan] … Name: [person:1] …",
  "categories": [{ "name": "ID", "tags": ["aadhaar", "pan", "…"] }, "…"],
  "tags": ["aadhaar", "pan", "…", "[person:1]", "[person:2]"],
  "people": ["[person:1]", "[person:2]"]
}
```

and expects (every field optional; `[person:n]` works in the title and
tags):

```json
{ "title": "PAN card - [person:1]", "category": "ID", "tags": ["pan", "[person:1]"],
  "doc_date": "2021-03-15", "expires": "" }
```

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
