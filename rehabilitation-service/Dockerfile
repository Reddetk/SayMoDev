# Образ migrate-бинарника (docs/deployment/migrations.md).
FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /
COPY --from=builder /out/migrate /migrate
COPY --from=builder /src/migrations /migrations

ENV MIGRATIONS_PATH=/migrations
ENTRYPOINT ["/migrate"]
CMD ["-action", "up"]
