.PHONY: build run test vet fmt clean tidy

BINARY ?= cursor-claude-connector

build:
	go build -o $(BINARY) ./cmd/cursor-claude-connector

run: build
	./$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
