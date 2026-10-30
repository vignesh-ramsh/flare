# Flare build helpers. Run `make help` for the list.

# The brand folder to build with: the official one, or your own (see brand/README.md).
BRAND_DIR ?= brand
# The Flutter app folder the branding files are generated into.
APP_DIR ?= app
# Set RELEASE=1 to refuse placeholder identifiers and URLs and a missing privacy policy.
RELEASE ?=

.DEFAULT_GOAL := help
.PHONY: help brand brand-test brand-check

help: ## Show this list
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-13s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

brand: ## Validate BRAND_DIR and generate the app's name, identifiers and icons from it
	cd app/tool/brand && dart pub get && dart run bin/brand.dart \
		--brand "$(abspath $(BRAND_DIR))" --app "$(abspath $(APP_DIR))" $(if $(RELEASE),--release)

brand-test: ## Analyze and test the brand tool
	cd app/tool/brand && dart pub get && dart analyze --fatal-infos && dart test

brand-check: ## Fail if app code or translations contain the project name
	sh app/tool/check_brand_neutral.sh
