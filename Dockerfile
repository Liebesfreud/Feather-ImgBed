FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/internal/app/web/dist ./internal/app/web/dist
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/feather-imgbed .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S feather && adduser -S -G feather feather
WORKDIR /app
COPY --from=builder /out/feather-imgbed /usr/local/bin/feather-imgbed
RUN mkdir -p /data && chown feather:feather /data
USER feather
VOLUME ["/data"]
EXPOSE 8080
ENV FEATHER_DATA_DIR=/data FEATHER_LISTEN=:8080 FEATHER_SECURE_COOKIE=true
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/feather-imgbed"]
