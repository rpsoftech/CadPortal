# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.27-alpine AS build
WORKDIR /src

# Dependencies first so they stay cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Static binaries: no C runtime is needed in the final image. Migrations are embedded.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && go build -trimpath -ldflags="-s -w" -o /out/adduser ./cmd/adduser \
 && mkdir -p /out/data/uploads

# ---- run ----
# Distroless: no shell, no package manager, CA certificates included (needed for R2 over TLS).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/api /out/adduser /app/
# Writable upload directory for STORAGE=local (development). Owned by the runtime user so a
# newly created volume mounted here inherits that ownership.
COPY --from=build --chown=65532:65532 /out/data /data

USER nonroot:nonroot
ENV GIN_MODE=release \
    HTTP_ADDR=:8080
EXPOSE 8080

# The image has no curl, so the API probes itself.
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=3 \
  CMD ["/app/api", "healthcheck"]

ENTRYPOINT ["/app/api"]
