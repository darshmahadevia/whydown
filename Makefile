.PHONY: build test verify eval live-eval live-integration
build:
	mkdir -p bin
	go build -o bin/whydown ./cmd/whydown
test:
	go test -race ./...
verify: test build
	go vet ./...
	python3 scripts/smoke.py
eval:
	go run ./cmd/whydown eval --provider offline
live-eval: build
	python3 scripts/live-eval.py

live-integration: build
	python3 scripts/live-integration.py
