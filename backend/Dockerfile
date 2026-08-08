# Build stage. Pinned to the minor version in go.mod rather than a floating
# "golang:alpine" so a new Go release cannot change the output of a build of an
# unchanged commit.
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies are copied and downloaded before the source, so editing a .go file
# does not invalidate the module cache layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a static binary, which is what makes the final stage a
# bare alpine rather than one carrying a libc the build happened to link against.
# -trimpath keeps build paths out of the binary; -s -w drop the symbol table and
# DWARF, which are of no use in a container that is not being debugged in place.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server .

# Runtime stage.
FROM alpine:3.24

# ca-certificates is not optional: without it, every outbound TLS call fails --
# OpenAI and Google Cloud Storage in production, with an error that reads like a
# network problem rather than a missing trust store.
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 app

COPY --from=build /out/server /usr/local/bin/server

# Non-root. The server binds 8080, not a privileged port, so there is nothing it
# needs root for -- and it writes no files at all, since uploads stream to
# storage rather than through the filesystem.
USER app

EXPOSE 8080

# No shell form and no entrypoint script: the binary is PID 1, so it receives
# SIGTERM directly. That matters here, because the shutdown path drains in-flight
# requests on that signal, and a shell wrapper would swallow it and leave the
# container to be killed after the grace period instead.
ENTRYPOINT ["/usr/local/bin/server"]
