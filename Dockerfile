FROM alpine:3.12.3

RUN apk add --no-cache ca-certificates

ADD ./app-checker /app-checker

ENTRYPOINT ["/app-checker"]
