# Multi-stage Dockerfile untuk runner pg-migrate
FROM golang:1.23-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/pg-migrate \
    ./cmd/pg-migrate

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 migrator

WORKDIR /app

COPY --from=builder /out/pg-migrate /usr/local/bin/pg-migrate
COPY databases.yaml ./databases.yaml
COPY databases/ ./databases/

USER migrator

ENTRYPOINT ["/usr/local/bin/pg-migrate"]
CMD ["version"]
