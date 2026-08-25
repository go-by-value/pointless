.PHONY: build install test lint vet clean

build:
	go build -trimpath -ldflags "-s -w" -o bin/pointless ./cmd/pointless

install:
	go install ./cmd/pointless

test:
	go test -race ./...

lint:
	golangci-lint run

# Run pointless on its own source.
vet: build
	go vet -vettool=bin/pointless ./...

clean:
	rm -rf bin
