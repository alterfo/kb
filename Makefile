.PHONY: check build test vet fmt

check: fmt vet test

build:
	go build -o bin/kb ./cmd/kb

fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -l .; echo "gofmt found unformatted files"; exit 1)

vet:
	go vet ./...

test:
	go test -timeout 120s ./...
