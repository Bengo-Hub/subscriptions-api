# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN GOTOOLCHAIN=auto go mod download
COPY . .
# Build api and seed binaries
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/subscription-api ./cmd/api && \
    GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/subscription-seed ./cmd/seed

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /bin/subscription-api /usr/local/bin/subscription-api
COPY --from=builder /bin/subscription-seed /usr/local/bin/seed
COPY internal/ent/migrate/migrations ./internal/ent/migrate/migrations
USER app
EXPOSE 4005
ENV PORT=4005
ENTRYPOINT ["/usr/local/bin/subscription-api"]
