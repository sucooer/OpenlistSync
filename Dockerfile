# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/openlist-sync ./cmd/openlist-sync

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata util-linux \
    && adduser -D -u 10001 openlist \
    && mkdir -p /data
COPY --from=build /out/openlist-sync /usr/local/bin/openlist-sync
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
WORKDIR /data
VOLUME ["/data"]
EXPOSE 18222
ENTRYPOINT ["/entrypoint.sh"]
CMD ["web"]