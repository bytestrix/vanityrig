.PHONY: build test lint

build:
	go build -o bin/vanityrig ./cmd/vanityrig

test:
	go test ./...

lint:
	go vet ./...
