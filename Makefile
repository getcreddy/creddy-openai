.PHONY: build test clean install dev

BINARY_NAME=creddy-openai
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -o bin/$(BINARY_NAME) .

build-all:
	GOOS=darwin GOARCH=arm64 go build -o bin/$(BINARY_NAME)-darwin-arm64 .
	GOOS=darwin GOARCH=amd64 go build -o bin/$(BINARY_NAME)-darwin-amd64 .
	GOOS=linux GOARCH=amd64 go build -o bin/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -o bin/$(BINARY_NAME)-linux-arm64 .

test:
	go test -v ./...

clean:
	rm -rf bin/

install: build
	mkdir -p ~/.creddy/plugins
	cp bin/$(BINARY_NAME) ~/.creddy/plugins/

info: build
	./bin/$(BINARY_NAME) info

scopes: build
	./bin/$(BINARY_NAME) scopes
