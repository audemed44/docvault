# Docvault

Self-hosted home for scanned family documents: iPhone Shortcut uploads, OCR search, private and family libraries.

One small Go binary with the web UI built in, data in SQLite.

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
```

See [docker-compose.example.yml](docker-compose.example.yml) for every option.

| Variable | Default | |
|---|---|---|
| `DOCVAULT_TOKEN` | (required) | What you sign in with; also Foyer's widget key |
| `DOCVAULT_DATA_DIR` | `/data` | Where the database lives |
| `DOCVAULT_PORT` | `8080` | Port inside the container |
| `HOMEPAGE_URL` | | Foyer's address, linked from the header |
| `DOCVAULT_DEBUG` | | Set to log debug messages |

## Foyer

Docvault serves a [Foyer](https://github.com/audemed44/foyer) card at
`/api/foyer/widget`:

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

```sh
cd frontend && npm install && npm run build && cd ..
DOCVAULT_TOKEN=dev DOCVAULT_DATA_DIR=./data go run ./cmd/docvault
# or, with hot reload: run the binary, then `npm run dev` in frontend/
```
