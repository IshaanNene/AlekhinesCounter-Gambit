# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.25-bookworm AS build
WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

# Build the service.
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/engine-worker ./services/engine-worker

# ---- runtime stage ----
FROM debian:bookworm-slim
# Stockfish is packaged in Debian; it installs to /usr/games/stockfish.
RUN apt-get update \
    && apt-get install -y --no-install-recommends stockfish ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/engine-worker /usr/local/bin/engine-worker

ENV ACG_ENGINE_ADDR=:50052 \
    ACG_STOCKFISH_PATH=/usr/games/stockfish
EXPOSE 50052
ENTRYPOINT ["engine-worker"]
