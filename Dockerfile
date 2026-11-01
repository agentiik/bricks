# One recipe for every brick of this catalog whose image is only its binary, selected by a
# build argument:
#
#     docker build --build-arg BRICK=jq -t ghcr.io/agentiik/jq:dev .
#
# One file rather than one per directory, because four files differing in a single word are
# four places for that word to be wrong. A brick needing more than this carries a Dockerfile of
# its own beside its manifest and says in it what it needs; http-request does, for the
# certificates it cannot verify a server without.
#
# The second stage is scratch. A brick is given a read-only root filesystem, no network by
# default and an unprivileged account, and an image with no shell, no package manager and no
# libc in it has nothing left to take advantage of.
# The build stage runs on the machine building, whatever platform the image is for, and the
# compiler crosses to the target: building the arm64 image under emulation would take a Go
# toolchain through QEMU for a binary Go can cross-compile in seconds.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
ARG TARGETOS
ARG TARGETARCH
ARG BRICK
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY ${BRICK} ./${BRICK}
# CGO_ENABLED=0 is what makes the binary static, and static is what makes scratch possible.
# -trimpath keeps the build path out of the binary, so two machines building this commit
# produce the same bytes.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/brick ./${BRICK}

FROM scratch
ARG BRICK
# The manifest is read off the image on first pull and cached by digest, so it travels in the
# image rather than beside it.
COPY ${BRICK}/brick.yaml /agk/brick.yaml
COPY --from=build /out/brick /usr/local/bin/brick
# The account every manifest of this catalog declares, and the one publication checks. Numeric
# because scratch has no passwd for a name to be looked up in.
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/brick"]
