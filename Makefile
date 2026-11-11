BIN_OUTPUT_PATH = bin
MODULE_BINARY = $(BIN_OUTPUT_PATH)/tdk-invensense
LDFLAGS = -s -w
# actionlint pins an older go.yaml.in/yaml/v4 than golangci-lint, so it can't share go.mod's tool graph.
ACTIONLINT = github.com/rhysd/actionlint/cmd/actionlint@v1.7.12

.PHONY: module build build-go lint lint-go test test-go clean-all license-check update

module: build
	rm -f $(BIN_OUTPUT_PATH)/module.tar.gz
	tar czf $(BIN_OUTPUT_PATH)/module.tar.gz $(MODULE_BINARY) meta.json

build: build-go

build-go:
	rm -f $(MODULE_BINARY)
	CGO_ENABLED=0 go build -tags osusergo,netgo -ldflags="$(LDFLAGS)" -o $(MODULE_BINARY) .

lint: lint-go
	go run $(ACTIONLINT)

lint-go:
	go mod tidy
	go vet -vettool="$$(go tool -n combined)" ./...
	GOGC=50 go tool golangci-lint run -v --fix --config=./etc/golangci.yaml

test: test-go

test-go:
	go tool gotestsum --format standard-verbose -- -race -count=1 ./...

clean-all:
	git clean -fxd

license-check:
	license_finder

update:
	go get go.viam.com/rdk@latest go.viam.com/api@latest
	go mod tidy
