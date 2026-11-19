# Compile on the builder CPU. An arm64 compiler under QEMU sits on this step with no output.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN set -eu; \
    ver=$(sh internal/version/closest.sh); \
    export CGO_ENABLED=0; \
    if [ -n "${TARGETOS}" ]; then export GOOS="${TARGETOS}"; fi; \
    if [ -n "${TARGETARCH}" ]; then export GOARCH="${TARGETARCH}"; fi; \
    go build -trimpath -ldflags="-s -w -X dlog/internal/version.Version=${ver}" -o /out/dlog-server ./cmd/server \
 && go build -trimpath -ldflags="-s -w -X dlog/internal/version.Version=${ver}" -o /out/dlog-agent ./cmd/agent \
 && install -d -m 0750 -o 65532 -g 65532 /out/var/lib/dlog

FROM gcr.io/distroless/static-debian12:nonroot AS server
COPY --from=build /out/dlog-server /usr/local/bin/dlog-server
COPY --from=build /out/var/lib /var/lib
ENV DLOG_DATA_DIR=/var/lib/dlog \
    DLOG_ADDR=:8080
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/dlog-server"]

FROM alpine:3.22 AS agent
RUN apk add --no-cache ca-certificates
COPY --from=build /out/dlog-agent /usr/local/bin/dlog-agent
ENV DLOG_STATE_DIR=/var/lib/dlog-agent \
    DLOG_DOCKER_HOST=/var/run/docker.sock
ENTRYPOINT ["/usr/local/bin/dlog-agent"]
