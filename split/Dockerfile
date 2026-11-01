# The brick of this catalog written as a shell script, so its image needs a shell and jq and
# nothing else: Alpine's, with jq added. No Agentiik library is in it.
#
#     docker build -f split/Dockerfile -t ghcr.io/agentiik/split:dev .
FROM alpine:3.22
RUN apk add --no-cache jq
COPY split/brick.yaml /agk/brick.yaml
COPY split/split.sh /usr/local/bin/split
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/split"]
