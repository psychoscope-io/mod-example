# Atalhos de desenvolvimento. `make check` e o que o CI e o hook pre-push executam.

.PHONY: build clean fmt lint test e2e check precommit hooks tag

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
ifeq ($(VERSION),)
VERSION := 0.0.0-dev
endif
DIST ?= dist

# Binario estatico (sem CGO): roda em qualquer distribuicao Linux.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(DIST)/mod-example ./cmd/mod-example

clean:
	rm -rf dist

fmt:
	gofmt -w .

# Verificacoes rapidas, usadas pelo hook pre-commit.
precommit:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "fora do padrao gofmt (rode make fmt):" >&2; echo "$$out" >&2; exit 1; fi
	go vet ./...

lint: precommit
	golangci-lint run ./...
	git ls-files -z '*.sh' '.githooks/*' | xargs -0 shellcheck
	actionlint

test:
	go test -race ./...

# Ponta a ponta contra um Core de verdade (../psychoscope-core ou PSYCHOSCOPE_CORE / PSYCHOSCOPE_BIN_DIR).
e2e:
	scripts/e2e.sh

check: lint test e2e

# Ativa os hooks versionados em .githooks (commit-msg, pre-commit, pre-push).
hooks:
	git config core.hooksPath .githooks

# Cria a tag anotada da proxima versao (interativo; so local, nao faz push). Veja scripts/tag.sh --help.
tag:
	scripts/tag.sh
