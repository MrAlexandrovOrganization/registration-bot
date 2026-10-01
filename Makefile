DOCKER_COMPOSE = docker compose
include versions.mk
BIN = $(CURDIR)/.bin

.PHONY: install install-hooks run format check test test-race build proto-gen proto-check config-check compose-build up down logs restart
install:
	go mod download
	GOBIN=$(BIN) go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	GOBIN=$(BIN) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v$(PROTOC_GEN_GO_GRPC_VERSION)
	GOBIN=$(BIN) go install golang.org/x/tools/cmd/goimports@v$(GOIMPORTS_VERSION)
	python3 -m venv .tools
	.tools/bin/pip install --disable-pip-version-check -r requirements-tools.txt
install-hooks:
	python3 scripts/install_hooks.py
proto-gen:
	PATH="$(BIN):$$PATH" protoc --go_out=. --go_opt=paths=source_relative --go_opt=Mapi/registration.proto=registration.local/frontend/api --go-grpc_out=. --go-grpc_opt=paths=source_relative --go-grpc_opt=Mapi/registration.proto=registration.local/frontend/api api/registration.proto
proto-check:
	python3 scripts/check_proto.py
run:
	go run ./cmd/telegram
format:
	$(BIN)/goimports -w cmd internal
	go fix ./...
	.tools/bin/ruff check --fix scripts
	.tools/bin/ruff format scripts
check: proto-check
	.tools/bin/ruff check scripts
	.tools/bin/ruff format --check scripts
	python3 scripts/check_format.py
	go vet ./...
	$(MAKE) test
	$(MAKE) config-check
	$(MAKE) secrets-check
test:
	go test ./... -count=1 -timeout=120s
test-race:
	go test -race ./... -count=1 -timeout=180s
build:
	go build -trimpath -o .bin/telegram ./cmd/telegram
config-check:
	$(DOCKER_COMPOSE) --env-file config/test.env config --quiet
compose-build:
	$(DOCKER_COMPOSE) build
up:
	$(DOCKER_COMPOSE) up -d --build
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
