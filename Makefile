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
.PHONY: help brand brand-test brand-check app-setup app-check app-integration app-notifications server server-test api-lint

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

app-check: brand-check ## Check formatting, analyze and test the Flutter app (set FLARE_SERVER_BIN to also test against the real server)
	# Only the files in git: the generated ones (gitignored) are not formatted.
	cd app && dart format --output=none --set-exit-if-changed $$(git ls-files --cached --others --exclude-standard -- 'lib/*.dart' 'test/*.dart' 'integration_test/*.dart')
	cd app && flutter analyze && flutter test

app-integration: server ## Run the app on Linux against the real server (needs xvfb, libsecret and a built app)
	cd app && HOME=$$(mktemp -d) FLARE_SERVER_BIN=$(abspath server/bin/flare) xvfb-run -a -s "-screen 0 1280x720x24" flutter test integration_test/app_test.dart -d linux

app-notifications: server ## Run the app on Linux against the real server and a real notification daemon (needs dunst, xvfb)
	cd app && HOME=$$(mktemp -d) FLARE_SERVER_BIN=$(abspath server/bin/flare) dbus-run-session -- xvfb-run -a -s "-screen 0 1280x720x24" bash -c 'dunst >/dev/null 2>&1 & sleep 1; flutter test integration_test/notifications_test.dart -d linux'

server: ## Build the Flare server and CLI into server/bin/flare
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/flare ./cmd/flare

server-test: ## Check formatting, vet and run the server's tests with the race detector
	@cd server && test -z "$$(gofmt -l .)" || { echo "These files need gofmt:"; gofmt -l .; exit 1; }
	cd server && go vet ./... && go test -race ./...

api-lint: ## Validate api/openapi.yaml and api/relay.yaml (needs Node.js)
	npx --yes @redocly/cli@2 lint api/openapi.yaml
	npx --yes @redocly/cli@2 lint api/relay.yaml
