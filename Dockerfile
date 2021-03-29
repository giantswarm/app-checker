FROM alpine:3.13.3

RUN apk add --no-cache ca-certificates

ADD ./app-checker /app-checker

ENTRYPOINT ["/app-checker"]
