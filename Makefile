.PHONY: run dev build test fmt

run:
	go run ./cmd/agentbar

dev:
	watchexec -r -e go -- sh -c 'go run ./cmd/agentbar < /dev/tty > /dev/tty 2>&1'

build:
	go build -o bin/agentbar ./cmd/agentbar

test:
	go test ./...

fmt:
	gofmt -w .
