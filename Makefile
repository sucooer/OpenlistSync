# openlist-sync 构建
#
# 本机若无 Go,所有目标都通过 docker golang 镜像完成。

GO_IMAGE  ?= golang:1.24-alpine
NAME      := openlist-sync
VERSION   := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
DIST      := dist

.PHONY: all build vet test image clean

all: build

# 当前平台二进制
build:
	@mkdir -p $(DIST)
	docker run --rm -v $(CURDIR):/work -w /work $(GO_IMAGE) \
		sh -c "CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$(NAME) ./cmd/openlist-sync"

# 交叉编译
#   make binary GOOS=darwin GOARCH=arm64
#   make binaries -> linux/amd64 + linux/arm64 + darwin/arm64 + darwin/amd64
binary:
	@mkdir -p $(DIST)
	docker run --rm -v $(CURDIR):/work -w /work $(GO_IMAGE) \
		sh -c "CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
			go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$(NAME)-$(GOOS)-$(GOARCH) ./cmd/openlist-sync"

binaries:
	@$(MAKE) binary GOOS=linux GOARCH=amd64
	@$(MAKE) binary GOOS=linux GOARCH=arm64
	@$(MAKE) binary GOOS=darwin GOARCH=amd64
	@$(MAKE) binary GOOS=darwin GOARCH=arm64
	@$(MAKE) binary GOOS=freebsd GOARCH=amd64

# 静态检查
vet:
	docker run --rm -v $(CURDIR):/work -w /work $(GO_IMAGE) sh -c "go vet ./..."

test:
	docker run --rm -v $(CURDIR):/work -w /work $(GO_IMAGE) sh -c "go test ./..."

# Docker 镜像
image:
	docker build --build-arg VERSION=$(VERSION) -t $(NAME):$(VERSION) .
	docker tag $(NAME):$(VERSION) $(NAME):latest

clean:
	rm -rf $(DIST)