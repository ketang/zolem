# Release-only Dockerfile: it exists solely for GoReleaser's build context,
# where the pre-built `zolem` binary is placed next to it. A plain local
# `docker build .` is unsupported (there is no zolem binary to COPY). To get a
# local image, run `goreleaser release --snapshot --clean`.
FROM gcr.io/distroless/static:nonroot
COPY zolem /zolem
ENTRYPOINT ["/zolem"]
