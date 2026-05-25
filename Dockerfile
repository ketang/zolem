FROM gcr.io/distroless/static:nonroot
COPY zolem /zolem
ENTRYPOINT ["/zolem"]
