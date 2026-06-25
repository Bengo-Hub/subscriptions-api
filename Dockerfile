# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN GOTOOLCHAIN=auto go mod download
COPY . .
# Build all binaries: api, migrate, seed
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/subscription-api ./cmd/api && \
    GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/subscription-migrate ./cmd/migrate && \
    GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/subscription-seed ./cmd/seed

FROM alpine:3.20
# ca-certificates: TLS trust for HTTPS remotes (S3/OneDrive/GDrive/WebDAV).
# rclone: powers pluggable backup-destination mirroring off the local PVC.
RUN apk add --no-cache ca-certificates tzdata rclone && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /bin/subscription-api /usr/local/bin/subscription-api
COPY --from=builder /bin/subscription-migrate /usr/local/bin/subscription-migrate
COPY --from=builder /bin/subscription-seed /usr/local/bin/subscription-seed
COPY internal/ent/migrate/migrations ./internal/ent/migrate/migrations
COPY scripts/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
USER app
EXPOSE 4005
ENV PORT=4005
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
