FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o zolem ./cmd/zolem

FROM gcr.io/distroless/base:nonroot
COPY --from=builder /src/zolem /zolem
ENTRYPOINT ["/zolem"]
