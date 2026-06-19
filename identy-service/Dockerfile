# Dockerfile -- migrate binary (multi-stage)
# Используется только для запуска миграций БД.
# Stage builder компилирует cmd/migrate/main.go.
# Stage runtime -- минимальный образ без Go toolchain.

# ---- Stage 1: builder -------------------------------------------------------
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Кэшируем зависимости отдельным слоем
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /app/bin/migrate \
    ./cmd/migrate/main.go

# ---- Stage 2: runtime -------------------------------------------------------
FROM gcr.io/distroless/static-debian12

WORKDIR /app

COPY --from=builder /app/bin/migrate  ./migrate
COPY --from=builder /app/migrations   ./migrations

ENV MIGRATIONS_PATH=./migrations

ENTRYPOINT ["./migrate"]
# CMD по умолчанию не задан -- action передаётся из docker-compose command: ["-action", "up"]
