# Regto Makefile —— 公开版构建入口
#
# 说明：本仓库是 Regto 的可运行产品代码子集，不含内部规格、文档与测试体系。
# 因此这里只保留不依赖那些目录的命令；构建、前端、迁移均可在干净克隆上执行。

GO      ?= go
WEB     ?= web
NPM     ?= npm
VERSION ?= 1.0.0

LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build check run fmt vet tidy generate clean \
        migrate-up migrate-down migrate-force \
        web-init web-dev web-gen web-build web-assets web-lint web-typecheck web-check

## all / check：干净克隆上的最小自检
all: check

check: fmt vet build

## build：先同步前端产物（若已安装依赖），再编出两个二进制
## go:embed 不能跨目录引用，webui 包必须有自己的 dist（见 webui.go 说明）。
build: web-assets
	@mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/regto-server ./cmd/regto-server
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/regtoctl ./cmd/regtoctl
	$(GO) build ./...

## run：启动 API + Web 同源服务（默认 127.0.0.1:8080）
run:
	$(GO) run ./cmd/regto-server

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

generate:
	$(GO) generate ./...

## 迁移：生产禁止自动迁移，服务启动只校验 schema 版本
## down 默认只回滚 1 步；全量回滚用 `down all`；dirty 修复用 make migrate-force V=<n>
migrate-up:
	$(GO) run ./cmd/migrate up

migrate-down:
	$(GO) run ./cmd/migrate down

migrate-force:
	$(GO) run ./cmd/migrate force $(V)

clean:
	rm -f cover.out
	rm -rf bin/

# ---------- Web UI（React 18 + Vite + AntD 5） ----------

web-init:
	cd $(WEB) && $(NPM) install --no-fund --no-audit

web-dev:
	cd $(WEB) && $(NPM) run dev

## web-gen：由 api/openapi/v1.yaml 生成 TS 类型（生成物入库，CI 校验零漂移）
web-gen:
	cd $(WEB) && $(NPM) run gen

web-build:
	cd $(WEB) && $(NPM) run build
	@rm -rf internal/adapter/http/webui/dist
	@mkdir -p internal/adapter/http/webui/dist
	@cp -r $(WEB)/dist/. internal/adapter/http/webui/dist/
	@touch $(WEB)/dist/.gitkeep internal/adapter/http/webui/dist/.gitkeep
	@echo "web-build：产物已同步到 internal/adapter/http/webui/dist（随二进制 embed）"

## web-assets：装了前端依赖才构建；干净克隆跳过，Go 侧仍可编译（页面需构建后才可用）
web-assets:
	@if [ -d "$(WEB)/node_modules" ] && command -v $(NPM) >/dev/null 2>&1; then $(MAKE) web-build; \
	elif [ ! -d "$(WEB)/node_modules" ]; then echo "web-assets：未安装前端依赖，跳过（先 make web-init）"; \
	else echo "web-assets：npm 不在 PATH，跳过前端构建"; fi

web-lint:
	cd $(WEB) && $(NPM) run lint

web-typecheck:
	cd $(WEB) && $(NPM) run typecheck

## web-check：类型、lint、契约生成一致性、构建
web-check: web-gen web-lint web-typecheck web-build
	@git diff --exit-code -- $(WEB)/src/api/generated || \
		{ echo "web-check：生成的 TS 类型与契约不一致，请提交 web/src/api/generated"; exit 1; }
