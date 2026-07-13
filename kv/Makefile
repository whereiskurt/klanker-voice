# kv — developer targets

.PHONY: build install test vet

## build: compile a local kv binary at kv/bin/kv
build:
	go build -o bin/kv ./cmd/kv

## install: install the kv binary onto your PATH (via GOBIN / GOPATH/bin)
install:
	go install ./cmd/kv

## test: run the kv test suite
test:
	go test ./...

## vet: run go vet across the kv module
vet:
	go vet ./...
