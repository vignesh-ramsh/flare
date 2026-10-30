# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Architecture and tech stack design ([docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)).
- Apache-2.0 licence, `NOTICE`, trademark policy, contribution guide, code of conduct, security policy,
  governance, and issue and pull request templates.
- White-label branding: the official brand folder (`brand/`), an example brand (`brand/examples/acme`),
  and a generator (`app/tool/brand`, run with `make brand`) that validates a brand folder and produces
  the app's name, identifiers and icons for Android, iOS, macOS, Windows and Linux. Release mode
  (`make brand RELEASE=1`) refuses placeholder identifiers and a missing privacy policy.
- CI for the brand tool, tested on the oldest supported Dart (3.9) and the current stable one, and a
  check that app code never hard-codes the project name.
