# syntax=docker/dockerfile:1

# One image, two programs. Choose the process with the container command:
#   docker run returnit /web      (HTTP API, port 8080; the default)
#   docker run returnit /worker   (background jobs, no port)
FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependencies first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/web ./cmd/web && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# Static binaries, no shell or package manager. Includes CA certificates (OIDC discovery over
# HTTPS) and runs as an unprivileged user. Timezone data is also embedded in the binaries, so
# WIB resolution never depends on the base image.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/web /out/worker /
EXPOSE 8080
CMD ["/web"]
