# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25

FROM golang:${GO_VERSION} AS builder
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags='-s -w' -o /out/server ./cmd/server
RUN go build -trimpath -ldflags='-s -w' -o /out/fakeprovider ./cmd/fakeprovider

FROM gcr.io/distroless/static-debian12:nonroot AS server
COPY --from=builder /out/server /usr/local/bin/server
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/usr/local/bin/server"]

# Fake rates provider — non-customer-facing artifact. Built for local
# docker-compose and AWS load-test environments where simulating tariff
# plans (e.g. Enterprise) without burning real upstream quota is required.
# Never deployed alongside production traffic.
FROM gcr.io/distroless/static-debian12:nonroot AS fakeprovider
COPY --from=builder /out/fakeprovider /usr/local/bin/fakeprovider
EXPOSE 9090
USER nonroot
ENTRYPOINT ["/usr/local/bin/fakeprovider"]
