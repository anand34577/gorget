# syntax=docker/dockerfile:1
# Gorget server: control plane, relay, STUN, WireGuard gateway and web console.

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/web/ui/dist ./internal/web/ui/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/anand34577/gorget/internal/core.Version=${VERSION}" -o /out/gorget-server ./cmd/gorget-server

FROM alpine:3.22
# nftables + iproute2 for the gateway; wireguard-go is built in and the kernel module is used when present.
RUN apk add --no-cache ca-certificates nftables iproute2 tzdata
COPY --from=build /out/gorget-server /usr/local/bin/gorget-server
ENV GORGET_DATA_DIR=/var/lib/gorget
VOLUME /var/lib/gorget
EXPOSE 443/tcp 443/udp 80/tcp 3478/udp 3479/udp 51820/udp
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["gorget-server", "healthcheck"]
ENTRYPOINT ["gorget-server"]
CMD ["serve"]
