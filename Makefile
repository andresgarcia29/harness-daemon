VERSION ?= 0.1.0
LDFLAGS := -X main.Version=$(VERSION)
.DEFAULT_GOAL := help

help: ## esta ayuda
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## compila para esta máquina
	@go build -ldflags "$(LDFLAGS)" -o bin/harnessd ./cmd/harnessd

test: ## vet + tests
	@go vet ./... && go test ./...

run: build ## arranca en primer plano
	@./bin/harnessd run

init: build ## arranca si no hay ninguno (idempotente: diez sesiones, un daemon)
	@./bin/harnessd ensure

status: build ## ¿quién tiene el puerto?
	@./bin/harnessd status

stop: build ## lo para (ojo: el daemon es global a todos tus workspaces)
	@./bin/harnessd stop

dist: ## binarios para las 4 plataformas (lo que consume el plugin)
	@for p in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" \
	    -o dist/harnessd-$$os-$$arch ./cmd/harnessd || exit 1; \
	  echo "  ✓ dist/harnessd-$$os-$$arch"; \
	done
	@cd dist && shasum -a 256 harnessd-* > SHA256SUMS && echo "  ✓ dist/SHA256SUMS"

clean: ## limpia
	@rm -rf bin dist

.PHONY: help build test run init status stop dist clean
