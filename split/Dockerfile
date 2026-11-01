# The brick of this catalog written as a shell script, so its image needs a shell and jq and
# nothing else: Alpine's, with jq added. No Agentiik library is in it.
#
#     docker build -f split/Dockerfile -t ghcr.io/agentiik/split:dev .
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk add --no-cache jq
COPY split/brick.yaml /agk/brick.yaml
COPY split/split.sh /usr/local/bin/split
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/split"]
