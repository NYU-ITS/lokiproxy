FROM --platform=$BUILDPLATFORM golang:1.24 AS build
ARG TARGETARCH
WORKDIR /usr/src/app
COPY *.go go.mod go.sum ./
COPY internal internal
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -tags netgo -ldflags -w -o bin/lokiproxy-$TARGETARCH lokiproxy.go

FROM alpine:latest
ARG TARGETARCH
COPY --from=build /usr/src/app/bin/lokiproxy-$TARGETARCH /usr/local/bin/lokiproxy
CMD lokiproxy
