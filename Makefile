VERSION ?= 0.1.0
LDFLAGS := -X main.Version=$(VERSION)
# harness-ui es la fuente de verdad del panel (ADR-0003, repo aparte). Su dist
# se embebe aquí vía //go:embed internal/webui/dist. `make ui` lo reconstruye.
UI_REPO ?= $(HOME)/Workspace/harness-ui
.DEFAULT_GOAL := help

help: ## esta ayuda
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

ui: ## reconstruye el embed del panel desde el repo harness-ui (ADR-0003)
	@test -d "$(UI_REPO)" || { echo "no encuentro harness-ui en $(UI_REPO) (set UI_REPO)"; exit 1; }
	@cd "$(UI_REPO)" && npm ci --silent && npm run build
	@rm -rf internal/webui/dist && cp -R "$(UI_REPO)/dist" internal/webui/dist
	@echo "  ✓ internal/webui/dist reconstruido desde harness-ui ($(UI_REPO))"

build: ## compila para esta máquina (bin/harness + symlink harnessd)
	@go build -ldflags "$(LDFLAGS)" -o bin/harness ./cmd/harnessd
	@ln -sf harness bin/harnessd

test: ## docs + vet + tests
	@./scripts/check-docs.sh && go vet ./... && go test ./...

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

.PHONY: help ui build test run init status stop dist clean
