# Docvault

Instructions for coding agents working in this repository. `CLAUDE.md`
imports this file.

## Project

Self-hosted home for scanned family documents: iPhone Shortcut uploads,
OCR search, private and family libraries (a CamScanner replacement). A Go
server (`cmd/docvault`, `internal/`) serves a JSON API and the Preact +
TypeScript frontend (`frontend/`), built into `web/dist` and embedded in
the binary. State lives in SQLite at `/data/docvault.db` (`internal/store`);
originals in `/data/files/<id>/`, derived files in `/data/cache/<id>/`.

- `internal/server`: routes (`server.go`), accounts, sessions, API tokens
  and the same-origin guard (`auth.go`), ingestion shared by every way in
  (`ingest.go`), documents and the upload endpoint (`documents.go`),
  folder import (`import.go`), categories and settings (`settings.go`),
  the Foyer card and Drop uploads (`foyer.go`).
- `internal/store`: schema, migrations (append-only, tracked in
  `PRAGMA user_version`) and queries. Documents with `owner_id` NULL are in
  the Family space; the extracted text lives only in the FTS5 table.
- `internal/process`: the background worker (one document at a time):
  photo → PDF (`pdf.go`, EXIF orientation applied by the page transform),
  page count, thumbnail, text layer or Tesseract OCR, then the optional
  classifier (`classify.go`: the input and Sanitize; `llm.go`: chat
  models through an OpenAI-compatible API).
- `internal/mask`: hides identifiers in text before it goes to a
  classifier. Anything new that sends document text off the server must
  go through it.
- `frontend/src`: `App.tsx` (session, shell, nav), `router.ts` (path
  routes), `api.ts` (one function per endpoint), `components/ui.tsx`
  (Dialog, Field, Figure, SectionHead, useAction), `styles.css`.

## Constraints

- **Low memory is a feature.** One static binary, `GOMEMLIMIT=32MiB`.
  poppler and tesseract run only as short child processes, one page at a
  time; OCR at 300 dpi peaks around 460 MB, hence `mem_limit: 768m`.
- **Originals are never rewritten.** Thumbnails, photo PDFs and text are
  derived and can be rebuilt ("Run OCR again").
- **Documents stay on the server.** The only thing that leaves is masked
  text for suggestions, when `DOCVAULT_LLM_KEY` (or
  `DOCVAULT_CLASSIFIER_URL`) is set; suggestions are only ever applied by
  a person. Tests use a fake model server, never a real key.
- Direct dependencies: modernc.org/sqlite (pure Go, so the build stays
  static and cgo-free). Justify any new one, Go or npm.
- Every `/api/` call needs a signed-in user: a session cookie, or a
  personal API token (`dv_…`, the iOS Shortcut) that can't touch account
  settings. Users only see their own documents and the Family space, admins
  included. `DOCVAULT_TOKEN` is not a user: it makes the first account and
  is Foyer's key (`/api/foyer/*`). Secrets are stored hashed. State-changing
  browser requests from another origin are refused (`sameOrigin`). Never
  log or return secrets.
- `GET /api/foyer/widget` serves the card in Foyer's widget format
  (https://github.com/audemed44/foyer/blob/main/docs/app-widgets.md).
- `HOMEPAGE_URL` puts a link back to Foyer in the header, for admins
  only: the family uses Docvault, not the rest of the homelab.
- UI style is Foyer's: Swiss editorial, always dark (no light theme), heavy
  Inter headlines, tracked uppercase eyebrows, 2px rules over numbered
  headings, square corners, one accent (#2563ff). Check phone width too
  (390px): it's used from an iPhone.

## Commits

Conventional Commits: `<type>(<scope>): <summary>`, e.g. `feat(items): ...`.
CI rejects anything else, including the PR title.

## Checks before pushing

```sh
go vet ./... && go test -race ./...        # needs web/dist (npm run build)
cd frontend && npm run format:check && npm run typecheck && npm test && npm run build
docker build -t docvault:dev .
```

The processing tests need poppler and tesseract (`eng`, `hin`) and skip
without them; CI installs them. To run them like the image does:

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.27-alpine sh -c \
  "apk add -q poppler-utils tesseract-ocr tesseract-ocr-data-eng tesseract-ocr-data-hin font-dejavu && go test ./internal/process/"
```
