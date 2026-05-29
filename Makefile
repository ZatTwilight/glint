.PHONY: run dev build test fmt

run:
	go run ./cmd/glint

build:
	go build -o bin/glint ./cmd/glint

test:
	go test ./...

fmt:
	gofmt -w .
