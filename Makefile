Version := $(shell git describe --tags --dirty)
GitCommit := $(shell git rev-parse HEAD)
LDFLAGS := "-s -w -X main.Version=$(Version) -X main.GitCommit=$(GitCommit)"
NAME := lokiproxy

.PHONY: all
all: dist

.PHONY: test
test:
	go test -race -v ./...

.PHONY: dist linux-amd64 darwin linux-armv7 linux-arm64 windows-amd64
dist: linux-amd64 darwin linux-armv7 linux-arm64 windows-amd64

linux-amd64:
	mkdir -p bin/
	GOARCH=amd64 CGO_ENABLED=0 GOOS=linux go build -ldflags $(LDFLAGS) -o bin/$(NAME)-amd64 lokiproxy.go

darwin:
	mkdir -p bin/
	GOARCH=amd64 CGO_ENABLED=0 GOOS=darwin go build -ldflags $(LDFLAGS) -o bin/$(NAME)-darwin lokiproxy.go

linux-armv7:
	mkdir -p bin/
	GOARM=7 GOARCH=arm CGO_ENABLED=0 GOOS=linux go build -ldflags $(LDFLAGS) -o bin/$(NAME)-arm lokiproxy.go

linux-arm64:
	mkdir -p bin/
	GOARCH=arm64 CGO_ENABLED=0 GOOS=linux go build -ldflags $(LDFLAGS) -o bin/$(NAME)-arm64 lokiproxy.go

windows-amd64:
	mkdir -p bin/
	GOARCH=amd64 GOOS=windows CGO_ENABLED=0 go build -ldflags $(LDFLAGS) -o bin/$(NAME).exe lokiproxy.go

.PHONY: image
image:
	docker buildx build --pull . --platform linux/amd64,linux/arm64 --push --tag ghcr.io/nyu-rts/lokiproxy:$(Version)
