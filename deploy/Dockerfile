FROM golang:1.27-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN set -eu; \
    ver=$(sh internal/version/closest.sh); \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X dlog/internal/version.Version=${ver}" -o /out/dlog-server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X dlog/internal/version.Version=${ver}" -o /out/dlog-agent ./cmd/agent

FROM alpine:3.22 AS server
RUN apk add --no-cache ca-certificates su-exec \
 && adduser -D -u 65532 -H dlog
COPY --from=build /out/dlog-server /usr/local/bin/dlog-server
COPY deploy/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
ENV DLOG_DATA_DIR=/var/lib/dlog \
    DLOG_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/docker-entrypoint.sh"]

FROM alpine:3.22 AS agent
RUN apk add --no-cache ca-certificates
COPY --from=build /out/dlog-agent /usr/local/bin/dlog-agent
ENV DLOG_STATE_DIR=/var/lib/dlog-agent \
    DLOG_DOCKER_HOST=/var/run/docker.sock \
    DLOG_AGENT_CERT=/etc/dlog/agent.crt \
    DLOG_AGENT_KEY=/etc/dlog/agent.key
ENTRYPOINT ["/usr/local/bin/dlog-agent"]
