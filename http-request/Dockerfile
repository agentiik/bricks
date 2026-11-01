# The one brick of this catalog that needs more than its binary, and the reason is one file: a
# scratch image carries no certificate authorities, and a client with none cannot verify any
# server it connects to. The alternative would be a client that does not verify, which would make
# network: egress and an allow list a decoration.
#
#     docker build -f http-request/Dockerfile -t ghcr.io/agentiik/http-request:dev .
#
# Everything else is the root Dockerfile's recipe, and the comments there say why.
# The build stage runs on the machine building, whatever platform the image is for, and the
# compiler crosses to the target: building the arm64 image under emulation would take a Go
# toolchain through QEMU for a binary Go can cross-compile in seconds.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY http-request ./http-request
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/brick ./http-request

FROM scratch
# Where Go looks for them on Linux, first of the paths it tries.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY http-request/brick.yaml /agk/brick.yaml
COPY --from=build /out/brick /usr/local/bin/brick
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/brick"]
