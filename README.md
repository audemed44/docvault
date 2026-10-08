# Docvault

Self-hosted home for scanned family documents: iPhone Shortcut uploads, OCR search, private and family libraries.

A CamScanner replacement for a family on one homelab. Scan on the iPhone,
**Share → Save to Vault**, and the PDF lands on your server. There it gets a
thumbnail, its text is pulled out (Tesseract OCR when it's a scan without a
text layer, in English and Hindi), and it becomes searchable. One small Go
binary with the web UI built in, data in SQLite, files on disk.

- **Accounts**: each person has a private library. A shared **Family**
  space holds what everyone should see. Admins add people; there's no
  sign-up and no passwords: the username is the sign-in (see below).
- **Uploads**: from the iOS Shortcut (it sends your username), the web UI
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
      - DOCVAULT_TOKEN=${DOCVAULT_TOKEN} # optional, for Foyer: openssl rand -hex 32
    volumes:
      - ./docvault:/data
    ports:
      - "8090:8080"
    mem_limit: 768m # idles at ~25 MB; OCR peaks ~460 MB per page, briefly
```

Then open it and create the first account (an admin), which adds the
others. See [docker-compose.example.yml](docker-compose.example.yml)
for every option.

| Variable | Default | |
|---|---|---|
| `DOCVAULT_TOKEN` | | Foyer's key for the widget and Drop (no Foyer card without it) |
| `DOCVAULT_DATA_DIR` | `/data` | Database, files, cache and import folders |
| `DOCVAULT_PORT` | `8080` | Port inside the container |
| `DOCVAULT_WORKERS` | `1` | Documents read (OCR'd) at once (up to 8) until Settings → Processing → Read at once changes it; each takes a core and up to ~500 MB |
| `HOMEPAGE_URL` | | Foyer's address, linked from the header (admins only) |
| `DOCVAULT_LLM_KEY` | | API key for suggestions from a chat model (below) |
| `DOCVAULT_LLM_MODEL` | | The model, e.g. an OpenRouter model ID |
| `DOCVAULT_LLM_URL` | `https://openrouter.ai/api/v1` | Any OpenAI-compatible API (llama.cpp, Ollama…) |
| `DOCVAULT_CLASSIFIER_URL` | | Or: your own service that takes the input as JSON (below) |
| `DOCVAULT_CLASSIFIER_TOKEN` | | Sent to it as a bearer token |
| `DOCVAULT_DEBUG` | | Set to log debug messages |

Uploads can be big (multi-page scans are 10–30 MB): allow at least 100 MB
request bodies in the reverse proxy.

## Accounts

Docvault is meant for one family, reached only over a private network
(Tailscale): **the username is the whole sign-in**. There are no passwords
and no sign-up. On a fresh install the sign-in page makes the first
account, an admin; admins add everyone else under Settings → People. Don't
put it on the open internet.

Scripts and the Shortcut send the username in the `X-Docvault-User`
header; the browser gets a session cookie when you sign in.

## The iPhone Shortcut

Build a Shortcut (or share one and put its iCloud link in Settings →
Processing); it asks for the username once:

```
Receive  PDFs, Images  from  Share Sheet
Get Contents of URL  https://docs.example.com/api/categories?format=names
   Headers: X-Docvault-User: <username>
Choose from List  (Contents of URL)
Ask for Input  "Title?"  (default: Current Date)
Get Contents of URL
   POST  https://docs.example.com/api/upload
   Headers:  X-Docvault-User: <username>
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
separate documents.

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
- **At once:** suggestions have their own workers, separate from OCR, so
  several documents go to the model at the same time (8 by default, up to
  32: Settings → Suggestions → At once). A document that was read already
  isn't read again for a suggestion.

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
  amounts are kept. Document names get the same treatment except the
  labelled-line rule, so "Address proof Pune" stays as it is while
  "Dad passport" becomes "[person:1] passport". It's a strong reduction, not anonymisation:
  unlabelled names and addresses, and what a document is about, still go
  through. **What the classifier saw** on each document shows exactly what
  was sent, and Settings has a box to try the masking on any text.
- **Your categories and tags** come back as they are: the request carries
  a JSON schema listing them, and anything else in those fields is dropped
  (Settings → Tags manages the list; people are tags too).
- **Never "Other"**: a document the model can't place gets no category,
  so it stays in the Inbox to sort by hand instead of disappearing into
  Other (which people can still choose).
- **New tags** are proposed separately (`new_tags`, at most 2, like
  `airline-ticket`) when nothing on the list fits. The model is shown the
  tags already used on documents so it reuses them instead of making
  variants. They show as "New tags" on the suggestion and are added with
  it; Settings → Tags lists tags in use that aren't on the list, to add.
- A document that couldn't be read (a broken or password-protected PDF)
  can still get a suggestion from its name.
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
  "people": ["[person:1]", "[person:2]"],
  "used_tags": ["airline-ticket", "…"]
}
```

and expects (every field optional; `[person:n]` works in the title and
tags):

```json
{ "title": "PAN card - [person:1]", "category": "ID", "tags": ["pan", "[person:1]"],
  "new_tags": [], "doc_date": "2021-03-15", "expires": "" }
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
DOCVAULT_DATA_DIR=./data go run ./cmd/docvault
# or, with hot reload: run the binary, then `npm run dev` in frontend/
```
