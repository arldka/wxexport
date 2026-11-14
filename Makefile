.PHONY: build test race e2e lint
export CGO_ENABLED=0

build:
	go build -o bin/wxexport ./cmd/wxexport

test:
	go vet ./...
	go test ./...

race:
	CGO_ENABLED=1 go test -race ./...

e2e:
	go test -tags e2e ./e2e

lint:
	@which staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed"
