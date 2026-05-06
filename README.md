# OpenScape

A self-hosted 360° photo gallery. Upload equirectangular panoramas and standard photos, organise them into galleries, and share them with anyone — publicly, via secret link, or password-protected.

## Features

- **360° viewer** — equirectangular panoramas rendered with [Photo Sphere Viewer v5](https://photo-sphere-viewer.js.org/) (auto-rotate, compass)
- **Smart detection** — 360° photos identified automatically via XMP metadata or 2:1 aspect ratio
- **Gallery visibility levels** — Public · Unlisted · Password-protected · Private (members-only)
- **Gallery sharing** — invite registered users as viewer or editor per gallery
- **Drag-and-drop reorder** — sort photos in any order via SortableJS
- **Thumbnail panning** — GPU-accelerated CSS animation previews panoramas on hover
- **First-run setup wizard** — no config files; create your admin account in the browser
- **Dark-first UI** — built with HTMX + templ SSR; no heavy JS framework

## Stack

| Layer | Technology |
|---|---|
| Language | Go 1.25 |
| Web framework | Echo v5 |
| Templates | templ (SSR) + HTMX |
| Database | PostgreSQL 17 |
| Migrations | golang-migrate (embedded) |
| Image processing | disintegration/imaging |
| 360° viewer | Photo Sphere Viewer v5 (ESM) |
| Auth | gorilla/sessions · bcrypt · CSRF |

## Quick start (Docker Compose)

### 1. Clone

```bash
git clone https://github.com/xblackbytesx/openscape.git
cd openscape
```

### 2. Create an environment file

```bash
cp .env.example .env
```

Edit `.env`:

```env
DB_PASSWORD=change_me
SESSION_SECRET=a-random-string-of-at-least-32-characters
DOCKER_ROOT=/opt/docker          # host path for persistent data
SECURE_COOKIES=true              # set false if not behind HTTPS
ALLOW_REGISTRATION=false         # set true to let anyone register
MAX_UPLOAD_MB=100
```

### 3. Create the data directories

```bash
mkdir -p /opt/docker/openscape/database
mkdir -p /opt/docker/openscape/uploads
```

### 4. Start

```bash
make up
```

The app is available at `http://localhost:8080`. On first visit you are redirected to `/setup` to create your admin account.

## Running behind a reverse proxy

OpenScape expects to sit behind Traefik (or any reverse proxy) that handles TLS. The compose file connects to an external `proxy` network — add your Traefik labels to `openscape-app` as needed.

Set `SECURE_COOKIES=true` in production so session and CSRF cookies are only sent over HTTPS.

### Large / slow uploads

The Go server itself does not impose a body read timeout — multipart parts are streamed straight to disk. The worker pool generates thumbnails and runs `ffprobe`/`ffmpeg` in the background, so the HTTP response returns as soon as bytes are durably saved.

Reverse-proxy timeouts are usually the bottleneck. For Traefik, the relevant defaults can cut off a 1 GB upload on a 1 Mbps link before it finishes. Either bump `respondingTimeouts` on the entry point globally:

```yaml
# traefik.yml
entryPoints:
  websecure:
    address: ":443"
    transport:
      respondingTimeouts:
        readTimeout: "0s"   # no per-request body read timeout
        idleTimeout: "180s"
```

…or, on the `openscape-app` service in `docker-compose.yml`, attach a per-route override and disable buffering for the upload endpoint:

```yaml
services:
  openscape-app:
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.openscape.rule=Host(`gallery.example.com`)"
      - "traefik.http.routers.openscape.entrypoints=websecure"
      - "traefik.http.routers.openscape.tls.certresolver=letsencrypt"
      - "traefik.http.services.openscape.loadbalancer.server.port=8080"
      # Stream uploads instead of buffering them in Traefik:
      - "traefik.http.middlewares.openscape-stream.buffering.maxRequestBodyBytes=0"
      - "traefik.http.middlewares.openscape-stream.buffering.memRequestBodyBytes=2097152"
      - "traefik.http.routers.openscape.middlewares=openscape-stream@docker"
```

If you're chaining several `@file` middlewares already (rate-limit, secure-headers, gzip-compress, etc.), append the new docker-defined middleware to the same comma-separated list:

```yaml
      - traefik.http.middlewares.openscape-stream.buffering.maxRequestBodyBytes=0
      - traefik.http.middlewares.openscape-stream.buffering.memRequestBodyBytes=2097152
      - traefik.http.routers.openscape-app-secure.middlewares=https-redirect@file,non-www@file,secure-headers@file,gzip-compress@file,openscape-stream@docker
```

For nginx, the equivalent is `client_max_body_size <MAX_UPLOAD_MB>m;`, `client_body_timeout 0;`, and `proxy_request_buffering off;` on the `location /admin/galleries/` block.

### Mobile / batch upload notes

The browser uploader runs a single tus upload at a time by default (sequential, with chunked resume). On a phone that's not a throughput limit — each upload already saturates whatever bandwidth the device has — and it keeps memory pressure low so the tab doesn't get killed mid-batch. If you're on a fast desktop link and want to bump it, edit `UPLOAD_CONCURRENCY` near the top of `web/static/js/app.js`; values above 3 will hammer mobile clients.

`MAX_UPLOAD_MB=2000` is the **server-side ceiling**, not a recommended upload size. Anything over a couple hundred MB on a mobile cellular link will be slow regardless of resumability — uploading a 1 GB video on a phone is a 30+ minute commitment even on 5G.

## Development

```bash
make dev     # starts hot-reload stack (air + templ generate on save)
make down    # stop all containers
make reset   # full teardown + clean restart (wipes DB volume)
make logs    # follow app logs
```

The dev stack mounts the source tree into the container so file changes are picked up immediately. templ files are regenerated automatically on save.

### Generating templ files locally

If you have `templ` installed locally:

```bash
make generate
```

## Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Full PostgreSQL connection string |
| `SESSION_SECRET` | yes | — | At least 32 characters; signs session cookies |
| `PORT` | no | `8080` | Port the app listens on |
| `SECURE_COOKIES` | no | `false` | Set `true` behind HTTPS |
| `ALLOW_REGISTRATION` | no | `false` | Allow public account creation |
| `MAX_UPLOAD_MB` | no | `100` | Maximum upload size in MB |
| `UPLOADS_PATH` | no | `/app/data/uploads` | Where originals and thumbs are stored |

## Gallery visibility

| Level | Who can view |
|---|---|
| **Public** | Anyone, listed on the home page |
| **Unlisted** | Anyone with the link |
| **Password-protected** | Anyone with the link + password |
| **Private** | Owner and invited members only |

## Photo formats

Accepted: JPEG · PNG · WebP · HEIC/HEIF

360° detection order:
1. XMP `GPano:ProjectionType = equirectangular`
2. Aspect ratio ≥ 1.9:1 (width / height)

## CI / Docker image

Images are built and pushed to the GitHub Container Registry on every push to `main` and on version tags:

```bash
docker pull ghcr.io/xblackbytesx/openscape:latest
```

Tags published: `latest` · `main` · `sha-<short>` · `v1.2.3` (on release tags)

## License

GPL-2.0
