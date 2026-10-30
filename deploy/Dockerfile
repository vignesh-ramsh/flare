# syntax=docker/dockerfile:1
#
# Builds the Flare server into a small image with nothing in it but the program.
# Build it from the repository root:
#
#   docker build -f deploy/Dockerfile -t flare .
#   docker build -f deploy/Dockerfile --build-arg VERSION=v1.0.0 -t flare:1.0.0 .

FROM golang:1.25 AS build
WORKDIR /src/server

# Dependencies first, so that they are cached until go.mod or go.sum change.
COPY server/go.mod server/go.sum ./
RUN go mod download

COPY server/ ./
ARG VERSION=dev
# CGO is off because the SQLite driver is pure Go: the result is one static file.
# -trimpath keeps build paths out of the binary, so the build is reproducible.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/flare ./cmd/flare
# The data folder, created here because the final image has no shell to do it.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/flare /usr/local/bin/flare
# Owned by the unprivileged user the server runs as; a volume mounted here for
# the first time inherits that ownership.
COPY --from=build --chown=nonroot:nonroot /out/data /var/lib/flare

ENV FLARE_DATABASE=sqlite:///var/lib/flare/flare.db \
    FLARE_LISTEN=:8080
VOLUME /var/lib/flare
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/flare"]
CMD ["serve"]
