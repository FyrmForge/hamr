# hamr CLI image, published to ghcr.io/fyrmforge/hamr by the release workflow.
# Defaults to `hamr mock-serve`; see docs/guide/pkg/mock-serve.md.
#
# The build stage runs on the builder's own platform and cross-compiles, so a
# multi-arch build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags "-X github.com/FyrmForge/hamr/internal/cli/cmd.version=${VERSION} -X github.com/FyrmForge/hamr/internal/cli/cmd.commit=${COMMIT}" \
    -o /hamr ./cmd/hamr

# No RUN in this stage: it would need emulation for the foreign arch. Certs
# come from the build stage instead of apk.
FROM alpine:3.21
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /hamr /usr/local/bin/hamr
EXPOSE 4500
ENTRYPOINT ["hamr"]
CMD ["mock-serve"]
