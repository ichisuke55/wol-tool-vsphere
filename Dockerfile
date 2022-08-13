FROM golang:latest as builder

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
WORKDIR /go/src
COPY . .
RUN go mod download
RUN go build \
    -o /go/bin/main \
    -ldflags '-s -w'

FROM alpine:latest
COPY --from=builder /go/bin/main /app/main

ENTRYPOINT ["/app/main"]
