# keelage-server image: one static binary on distroless. goreleaser
# (dockers_v2) builds the context with the binaries under $TARGETPLATFORM/.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/keelage-server /keelage-server
USER nonroot:nonroot
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/keelage-server"]
CMD ["serve", "--addr", "0.0.0.0:8080", "--key", "/data/server.ed25519"]
