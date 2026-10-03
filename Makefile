.PHONY: build test lint demo

build:
	go build -o bin/vanityrig ./cmd/vanityrig

test:
	go test ./...

lint:
	go vet ./...

# Regenerates docs/demo.gif — needs vhs (https://github.com/charmbracelet/vhs).
demo:
	vhs docs/demo.tape
