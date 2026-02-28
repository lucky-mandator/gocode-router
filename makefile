GO      ?= go
BINARY  ?= bin/gocode-router
CONFIG  ?= config.yaml

.PHONY: build
build:
	@echo "==> Building gocode-router (output: $(BINARY))"
	@mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) -v .
	@echo "==> Build complete"

.PHONY: run
run: build
	@echo "==> Starting gocode-router with config $(CONFIG)"
	$(BINARY) serve --config $(CONFIG)

.PHONY: test
test:
	@echo "==> Running tests"
	$(GO) test ./...
	@echo "==> Tests finished"

.PHONY: clean
clean:
	@echo "==> Cleaning build artifacts"
	@rm -rf $(BINARY)
	@echo "==> Clean complete"
