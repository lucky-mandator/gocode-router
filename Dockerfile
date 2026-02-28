ARG GO_VERSION=1.25.1
FROM golang:${GO_VERSION} AS builder

WORKDIR /build

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o /main main.go

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /etc/gocode-router

COPY example.config.yaml /etc/gocode-router/config.yaml

COPY --from=builder /main /bin/main

ENTRYPOINT ["/bin/main"]
CMD ["serve", "--config", "/etc/gocode-router/config.yaml"]
