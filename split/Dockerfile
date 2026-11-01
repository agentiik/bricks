# The brick of this catalog written as a shell script, so its image needs a shell and jq and
# nothing else: Alpine's, with jq added. No Agentiik library is in it.
#
#     docker build -f split/Dockerfile -t ghcr.io/agentiik/split:dev .
FROM alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f
RUN apk add --no-cache jq
COPY split/brick.yaml /agk/brick.yaml
COPY split/split.sh /usr/local/bin/split
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/split"]
