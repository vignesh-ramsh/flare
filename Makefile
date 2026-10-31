# Flare build helpers. Run `make help` for the list.

# The brand folder to build with: the official one, or your own (see brand/README.md).
BRAND_DIR ?= brand
# The Flutter app folder the branding files are generated into.
APP_DIR ?= app
# Set RELEASE=1 to refuse placeholder identifiers and URLs and a missing privacy policy.
RELEASE ?=

# Version stamped into the server binary: the nearest git tag, or the commit.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.DEFAULT_GOAL := help
.PHONY: help brand brand-test brand-check app-setup app-check server server-test api-lint

help: ## Show this list
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-13s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

brand: ## Validate BRAND_DIR and generate the app's name, identifiers and icons from it
	cd app/tool/brand && dart pub get && dart run bin/brand.dart \
		--brand "$(abspath $(BRAND_DIR))" --app "$(abspath $(APP_DIR))" $(if $(RELEASE),--release)

brand-test: ## Analyze and test the brand tool
	cd app/tool/brand && dart pub get && dart analyze --fatal-infos && dart test

brand-check: ## Fail if app code or translations contain the project name
	sh app/tool/check_brand_neutral.sh

app-setup: brand ## Generate what the app needs to compile: brand files, translations, database code
	cd app && flutter pub get && dart run build_runner build

app-check: brand-check ## Check formatting, analyze and test the Flutter app (run app-setup first)
	# Only the files in git: the generated ones (gitignored) are not formatted.
	cd app && dart format --output=none --set-exit-if-changed $$(git ls-files --cached --others --exclude-standard -- 'lib/*.dart' 'test/*.dart')
	cd app && flutter analyze && flutter test

server: ## Build the Flare server and CLI into server/bin/flare
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/flare ./cmd/flare

server-test: ## Check formatting, vet and run the server's tests with the race detector
	@cd server && test -z "$$(gofmt -l .)" || { echo "These files need gofmt:"; gofmt -l .; exit 1; }
	cd server && go vet ./... && go test -race ./...

api-lint: ## Validate api/openapi.yaml and api/relay.yaml (needs Node.js)
	npx --yes @redocly/cli@2 lint api/openapi.yaml
	npx --yes @redocly/cli@2 lint api/relay.yaml
