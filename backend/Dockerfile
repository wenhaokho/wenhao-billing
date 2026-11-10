# The production image: a static binary on distroless. Everything the build
# needs is in this repository (gokit is vendored), so it downloads nothing
# private and needs no GitHub credential.
FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/compare ./cmd/compare

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
# /compare runs the read comparison inside Coolify's network (plan 11, Task 8).
COPY --from=build /out/compare /compare
EXPOSE 8000
# Distroless has no shell or curl: the binary probes itself.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/api", "healthcheck"]
ENTRYPOINT ["/api"]
