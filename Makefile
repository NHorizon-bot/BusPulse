.PHONY: run build test lint tidy

MODULE := github.com/buspulse/diagnose-agent
BINARY := bin/diagnose-agent

## 启动开发服务
run:
	go run ./cmd/server/...

## 编译二进制（使用 Go 1.26，CGO_ENABLED=0 保证在 macOS ARM64 上无 LC_UUID 问题）
GO := /opt/homebrew/Cellar/go/1.26.4/libexec/bin/go

build:
	CGO_ENABLED=0 $(GO) build -o $(BINARY) ./cmd/server/...

run:
	$(GO) run ./cmd/server/...

test:
	$(GO) test -race -count=1 ./...

## 运行测试（含竞态检测）
test:
	go test -race -count=1 ./...

## 运行 golangci-lint
lint:
	golangci-lint run ./...

## 整理依赖
tidy:
	go mod tidy

## 清理产物
clean:
	rm -rf bin/
