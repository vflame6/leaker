FROM golang:1.25 AS go_builder

WORKDIR /build

COPY go.mod ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o leaker

FROM alpine:3.21

WORKDIR /app

RUN mkdir -p /app/config
RUN adduser -D -u 1000 -s /sbin/nologin app
COPY --chown=app:app docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

USER app

COPY --from=go_builder /build/leaker /app/leaker

ENTRYPOINT ["/app/leaker"]
