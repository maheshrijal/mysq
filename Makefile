.PHONY: build test test-race e2e check

build:
	go build -trimpath -o bin/mysq ./cmd/mysq

test:
	go test ./...

test-race:
	go test -race ./...

e2e:
	bash test/e2e/run.sh

check:
	go vet ./...
	go test -race ./...
	go build -trimpath -o bin/mysq ./cmd/mysq
