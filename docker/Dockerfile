FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git curl nodejs npm

WORKDIR /app

# Install front-end deps into /tmp (avoids stale-snapshot cache issues with bind-mount zone)
COPY web/package.json /tmp/web/
RUN npm install --prefix /tmp/web && \
    mkdir -p web/static/vendor/psv && \
    cp /tmp/web/node_modules/htmx.org/dist/htmx.min.js              web/static/vendor/htmx.min.js && \
    cp /tmp/web/node_modules/sortablejs/Sortable.min.js              web/static/vendor/sortable.min.js && \
    cp /tmp/web/node_modules/three/build/three.module.min.js         web/static/vendor/psv/three.module.js && \
    cp /tmp/web/node_modules/@photo-sphere-viewer/core/index.module.js \
                                                                      web/static/vendor/psv/core.esm.js && \
    cp /tmp/web/node_modules/@photo-sphere-viewer/autorotate-plugin/index.module.js \
                                                                      web/static/vendor/psv/autorotate.esm.js && \
    cp /tmp/web/node_modules/@photo-sphere-viewer/compass-plugin/index.module.js \
                                                                      web/static/vendor/psv/compass.esm.js && \
    CSS=$(find /tmp/web/node_modules/@photo-sphere-viewer/core -maxdepth 3 -name "*.css" | head -1) && \
    cp "$CSS" web/static/vendor/psv/photo-sphere-viewer.css && \
    rm -rf /tmp/web

# Cache Go dependencies
COPY go.mod ./
RUN go mod download

COPY . .

# Generate templ files
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1001 && \
    templ generate

# Regenerate go.sum and download any missing modules
RUN go mod tidy

RUN CGO_ENABLED=0 GOOS=linux go build -o openscape ./cmd/openscape

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 1000 openscape && \
    adduser -u 1000 -G openscape -D -H openscape

WORKDIR /app

COPY --from=builder /app/openscape .
COPY --from=builder /app/web/static ./web/static

RUN mkdir -p /app/data/uploads && \
    chown -R openscape:openscape /app

USER openscape

EXPOSE 8080

CMD ["./openscape"]
