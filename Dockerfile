# Build
FROM golang:1.27-alpine AS go_builder
ENV CGO_ENABLED=0
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION is the release tag; CI and releases pass it with --build-arg.
ARG VERSION=dev
RUN go build -trimpath -ldflags "-s -w -X github.com/vflame6/leaker/cmd.VERSION=${VERSION}" -o leaker .

# Release
FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 -s /sbin/nologin app
USER app
COPY --from=go_builder /app/leaker /usr/local/bin/leaker

ENTRYPOINT ["leaker"]
