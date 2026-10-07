GO ?= go

.PHONY: build test lint integration check prepare-image web-check web-build
build:
	$(GO) build -buildvcs=false -trimpath -o bin/adtr ./cmd/adtr
test:
	python3 -m unittest discover -s scripts -p '*_contract.py'
	$(GO) test -race -count=1 ./...
lint:
	python3 scripts/verify_requirements.py
	test -z "$$(gofmt -l cmd internal)"
	$(GO) vet ./...
integration:
	$(GO) test -race -count=1 -tags=integration ./...
web-build:
	npm ci --prefix web --cache /tmp/adtr-npm-cache
	npm run build --prefix web
web-check: web-build
	npm run typecheck --prefix web
	npm test --prefix web
	npm audit --prefix web --audit-level=high --cache /tmp/adtr-npm-cache
check: lint test build web-check
prepare-image: web-build
	$(GO) mod download
	$(GO) mod verify
	$(GO) mod vendor

