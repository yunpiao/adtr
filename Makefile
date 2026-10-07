GO ?= go

.PHONY: build test lint integration check prepare-image
build:
	$(GO) build -buildvcs=false -trimpath -o bin/adtr ./cmd/adtr
test:
	python3 -m unittest discover -s scripts -p '*_contract.py'
	$(GO) test -race -count=1 ./...
lint:
	test -z "$$(gofmt -l cmd internal)"
	$(GO) vet ./...
integration:
	$(GO) test -race -count=1 -tags=integration ./internal/store
check: lint test build
prepare-image:
	$(GO) mod download
	$(GO) mod verify
	$(GO) mod vendor

