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
	GO="$(GO)" python3 scripts/run_integration.py
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


# Explicit CI-only binary for the three owned running-cancel acceptance suites.
# Never add this tag to build/check or to deployment artifacts.
.PHONY: build-ci-running-cancel
build-ci-running-cancel:
	test "$$(uname -s)" = Linux
	test "$$(uname -m)" = x86_64
	$(GO) test -count=1 ./internal/dbadmissioncheck
	GOOS=linux GOARCH=amd64 $(GO) build -tags=adtr_ci_dbadmission -buildvcs=false -trimpath -o bin/adtr ./cmd/adtr

.PHONY: test-owned-rescue-ci
test-owned-rescue-ci:
	test "$$(uname -s)" = Linux
	ADTR_RUN_OWNED_RESCUE_TESTS=1 python3 scripts/test_owned_rescue_runner_runtime.py
