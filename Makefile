.PHONY: build test lint ci

build:
	go build ./...

test:
	go test ./... -v -race -count=1

lint:
	golangci-lint run ./...

ci: build test lint
