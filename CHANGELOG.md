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
- The Flare server and command-line client (`server/`): publishing over HTTP; subscribing by streaming
  JSON, Server-Sent Events and WebSocket; catching up on missed messages without gaps or repeats; SQLite
  storage; graceful shutdown; `flare serve`, `flare publish` and `flare subscribe`. The HTTP contract is in
  `api/openapi.yaml`. Rate limits and push notifications are not implemented yet.
- A container image, Docker Compose file, Caddyfile and sample configuration (`deploy/`), and CI for the
  server on the minimum and the latest Go.
- Accounts: users with Argon2id passwords, access tokens (`flr_...`, stored only as hashes), and
  per-topic access rules with patterns, where the most specific rule wins. `flare user`, `flare token`,
  `flare access` and `flare login` manage them; `POST /v1/auth/login` and `/v1/account` serve apps.
  Streams and WebSockets end when the token, user or rule they depend on is withdrawn. Failed logins
  are limited per client, and `trusted-proxies` says which proxies to believe about who that is. An
  anonymous visitor who is refused gets `401` and a logged-in user `403`; wrong credentials are never
  treated as anonymous. The sample configuration now starts with `auth.default-access: deny-all`.
- Push notifications (server side): apps register devices (`POST /v1/devices`) and the topics to be woken for
  (`PUT /v1/devices/{id}/subscriptions`); publishing queues a notification for each in the same transaction
  that stores the message, and a dispatcher sends them with retries until the message expires. Senders for
  Web Push (RFC 8291 and 8292, checked against the RFC's test vector), UnifiedPush, FCM and APNs; the last two
  have been tested against fakes only. `GET /v1/messages/{id}` fetches one message after a wake-up.
  `push.content-free` keeps message content out of push services. Endpoints that users supply must be https
  at a public address, checked when connecting.
- The push relay (`flare relay`, [api/relay.yaml](api/relay.yaml)): a small server run by an app's publisher
  that holds its Firebase and Apple keys and wakes devices for Flare servers that don't have them, telling
  the server nothing of the push token and being told nothing of the topic or content. Apps register a
  token for an opaque handle; servers list the relays they trust under `push.allowed-relays`. Handles are
  kept as hashes, wake-ups are rate limited per handle and per address, and a device the push service has
  dropped is forgotten by both the relay and the server. There is no official relay yet.
- CI for the brand tool, tested on the oldest supported Dart (3.9) and the current stable one, and a
  check that app code never hard-codes the project name.
- The Flutter app, first skeleton (`app/`, [docs/APP-DESIGN.md](docs/APP-DESIGN.md)): Material 3 with the
  brand colour, one route tree, and a layout that follows the window's width (bottom bar, rail, or a sidebar
  with a details pane); an on-device database (drift) with subscriptions, messages, cursors and read
  markers; the topic list, thread, Subscribe, Inbox and Settings screens; English translations with
  `{appName}` in place of the name. Built and run on Linux, which is also named from the brand files; the
  other platforms are created but not wired to the brand yet. `make
  app-setup` and `make app-check` set it up and check it, and CI runs them.
- The app's connection to a server (`app/lib/net`): one WebSocket per server, resuming each topic from its
  cursor so a reconnect catches up without gaps or repeats, retrying with a growing, randomised wait, and
  telling the user when a server cannot be reached or refuses the saved sign-in or a topic. The app now
  receives and keeps messages. Tested against an in-process fake and against the real server (CI builds
  it); there is no sign-in screen yet, so connections are anonymous.
- Native projects: Android, iOS, macOS and Windows take their name and identifier from the brand output, and CI
  builds all five platforms and checks each build's name and identifier (nothing has been run on a device or
  an emulator). Servers may be reached over plain http as well as https, as in ntfy; an address typed without
  a scheme means https and the app's examples show https. macOS gained the outgoing-network entitlement and
  Android the INTERNET permission in release builds, without which the app could not reach any server.
