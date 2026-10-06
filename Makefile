DOCKER_COMPOSE = docker compose
include versions.mk
BIN = $(CURDIR)/.bin

.PHONY: install install-proto install-hooks run format check test test-race build proto-gen proto-check config-check compose-build up down logs restart
install: install-proto
	go mod download
	GOBIN=$(BIN) go install golang.org/x/tools/cmd/goimports@v$(GOIMPORTS_VERSION)
	python3 -m venv .tools
	.tools/bin/pip install --disable-pip-version-check -r requirements-tools.txt
install-proto:
	GOBIN=$(BIN) go install github.com/bufbuild/buf/cmd/buf@v$(BUF_VERSION)
	GOBIN=$(BIN) go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	GOBIN=$(BIN) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v$(PROTOC_GEN_GO_GRPC_VERSION)
install-hooks:
	python3 scripts/install_hooks.py
proto-gen:
	PATH="$(BIN):$$PATH" $(BIN)/buf generate
proto-check:
	$(BIN)/buf build
run: proto-gen
	go run ./cmd/telegram
format: proto-gen
	$(BIN)/goimports -w cmd internal
	go fix ./...
	.tools/bin/ruff check --fix scripts
	.tools/bin/ruff format scripts
check: proto-check proto-gen
	.tools/bin/ruff check scripts
	.tools/bin/ruff format --check scripts
	python3 scripts/check_format.py
	go vet ./...
	$(MAKE) test
	$(MAKE) config-check
	$(MAKE) secrets-check
test: proto-gen
	go test ./... -count=1 -timeout=120s
test-race: proto-gen
	go test -race ./... -count=1 -timeout=180s
build: proto-gen
	go build -trimpath -o .bin/telegram ./cmd/telegram
config-check:
	env -u BOT_TOKEN -u BACKEND_TOKEN -u TELEGRAM_WEBHOOK_SECRET WEBHOOK_URL= $(DOCKER_COMPOSE) --env-file config/test.env config --quiet
	env -u BOT_TOKEN -u BACKEND_TOKEN -u TELEGRAM_WEBHOOK_SECRET WEBHOOK_URL=https://example.invalid/telegram $(DOCKER_COMPOSE) --env-file config/test.env config --quiet
compose-build:
	$(DOCKER_COMPOSE) build
up:
	$(DOCKER_COMPOSE) up -d --build --wait --wait-timeout 180
down:
	$(DOCKER_COMPOSE) down
logs:
	$(DOCKER_COMPOSE) logs -f --tail=100
restart:
	$(DOCKER_COMPOSE) restart
secrets-check:
	python3 scripts/check_secrets.py

.PHONY: versions
versions:
	@$(foreach v,$(VERSION_VARS),printf '%s=%s\n' '$(v)' '$($(v))';)

.PHONY: register-webhook delete-webhook
register-webhook:
	$(DOCKER_COMPOSE) run --rm --no-deps telegram register-webhook
delete-webhook:
	$(DOCKER_COMPOSE) run --rm --no-deps telegram delete-webhook
