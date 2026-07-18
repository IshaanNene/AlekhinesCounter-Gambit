# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/fanout ./services/fanout

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/fanout /usr/local/bin/fanout

ENV ACG_FANOUT_ADDR=:8090
EXPOSE 8090
ENTRYPOINT ["fanout"]
