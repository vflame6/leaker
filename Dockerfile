# Build
FROM golang:1.25-alpine AS go_builder
RUN apk add build-base
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o leaker .

# Release
FROM alpine:latest
RUN apk upgrade --no-cache \
    && apk add --no-cache bind-tools ca-certificates
RUN adduser -D -u 1000 -s /sbin/nologin app
USER app
COPY --from=go_builder /app/leaker /usr/local/bin

ENTRYPOINT ["leaker"]
