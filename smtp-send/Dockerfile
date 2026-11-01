# A brick that needs more than its binary, for the reason http-request does: a mail server is
# reached over TLS, and a scratch image carries no certificate authorities, so a client with none
# could verify no server. The alternative would be a client that does not verify, which would make
# network: egress and an allow list a decoration.
#
#     docker build -f smtp-send/Dockerfile -t ghcr.io/agentiik/smtp-send:dev .
#
# Everything else is the root Dockerfile's recipe, and the comments there say why.
# The build stage runs on the machine building, whatever platform the image is for, and the
# compiler crosses to the target: building the arm64 image under emulation would take a Go
# toolchain through QEMU for a binary Go can cross-compile in seconds.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY smtp-send ./smtp-send
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/brick ./smtp-send

FROM scratch
# Where Go looks for them on Linux, first of the paths it tries.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY smtp-send/brick.yaml /agk/brick.yaml
COPY --from=build /out/brick /usr/local/bin/brick
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/brick"]
