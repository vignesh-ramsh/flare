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

- Sign-in: first-run pages (choose a server, which the app checks, then sign in or skip), a sign-in form
  that warns before sending a password over plain http, a Servers screen with each server's account,
  connection and Remove, and a Sign in button on the banners for a refused login or a topic that needs
  one. Tokens are kept in the platform's secure storage, never in the database; where there is none (a Linux
  desktop with no keyring) the app says so and keeps the sign-in until it closes. Signing out revokes the
  token on the server. The REST client (`config`, `login`, `revokeToken`) is tested against the real server.
- `make app-integration` runs the whole app on Linux, nothing replaced, against the real server. It found
  two bugs no widget test could: a startup that never finished (Riverpod 3 only runs a provider while
  something listens to it) and a program name Flutter's tool could not find.
- The brand tool now keeps `set(BINARY_NAME "...")` in the Linux and Windows projects current, because
  Flutter's tool reads that literal line to find the program to run.
- Desktop notifications (Windows, macOS, Linux): a message that arrives becomes a notification, by priority
  (1 listed only, 2 quiet, 3 normal, 4 and 5 stay until dismissed), unless the topic is muted or below its minimum
  priority, notifications are paused, or the user is reading that thread. History fetched on a first
  subscription is stored without a notification each, and a burst becomes one. Settings > Notifications and a
  per-topic bell set the choices; a system tray (open, the four latest messages, pause, quit) keeps the app
  running when the window is closed, but only where a tray exists. Verified on Linux against a real
  notification daemon (`make app-notifications`); Windows and macOS build in CI and are not yet run.
- The brand tool exposes `Brand.windowsIdentity` for Windows toast notifications.
- Replying from the app: a composer under every thread (text, and optionally a title and a priority) that
  sends over REST and shows the message as the user's own bubble; failures say why and keep the text. Database
  schema 3 records which messages are the user's own, and they never notify.
- A better Subscribe form: a menu of known servers, a random topic name, a note about whether the server is
  open, read-only or private, and filling in both fields from a pasted subscribe link or topic address. A
  topic's info page (details pane on large windows) shares the subscribe link and a QR code, and can
  unsubscribe. A message's link opens in the browser, for `http` and `https` only.
- Subscribe links, `<scheme>://subscribe/<host>/<topic>`, registered on Android and iOS and read from the
  command line on the desktop, open the Subscribe form filled in. The Android and iOS builds are checked in CI
  for the registered scheme.
- Fixed: the notification plugin's failure without a D-Bus session bus was left as an unhandled error; CI
  failures of the desktop notification commit (the tray plugin's native build in the test job, Android core
  library desugaring, and the integration target running the dunst test).
- Themes: Settings > Appearance lets users follow the system or choose a theme. A theme is a unique name,
  light or dark, and any of 32 colours (background, surface, border, text and so on); colours left out are
  derived from its primary, and a theme whose text could not be read is never offered. The brand folder's new
  `themes.yaml` lists the themes a build ships (the official brand has five, the Acme example two and its own
  defaults), validated by `make brand`. The colours are listed in `api/theme-tokens.json`, which the app, the
  brand tool and the server check themselves against.
- Fixed: a subscribe link opened on the first run could leave the user on the welcome page.
- Server themes: a server holds up to 50 colour themes that its administrators add, change and delete
  (`POST /v1/admin/themes`, `PUT` and `DELETE /v1/admin/themes/{name}`), and apps list publicly with
  `GET /v1/themes` (with an `ETag`), next to a `themes` feature in `/v1/config`. Themes use the same unique
  names and colours as the app's own, and the same rule that text must stay readable. `GET /v1/themes/schema`
  describes the colours for an editor.
- The app downloads each server's themes and keeps them on the device (database schema 4), asking again when it
  starts, when a server is added, when it returns to the foreground and when Appearance opens; an unchanged list
  is a 304. They are listed in Settings > Appearance under the server's name, work offline, and are dropped from
  the choice if the server drops them.
- Server settings an administrator may change while the server runs (`GET /v1/admin/settings`,
  `PUT` and `DELETE /v1/admin/settings/{key}`): the server's name, accent colour, app link scheme and public
  address; what anyone may do by default; the largest message and how long messages are kept; and the push
  switches. A change is checked together with the whole configuration, kept in the database, and shown beside
  what the configuration file says, which resetting goes back to. Most apply at once; push is set up when the
  server starts, so those say a restart is needed and show when what is running differs from what is set.
  A stored choice that no longer applies is left out with a warning instead of stopping the server. Where the
  server listens, its database, trusted proxies and push credentials stay in the file.
- Managing users from the Admin API: list, create, and change users' roles and passwords, disable and enable
  them, delete them (`/v1/admin/users`), list and revoke a user's tokens, and list, grant and remove access
  rules (`/v1/admin/access`). A disabled user cannot log in, their tokens stop working and what they have open
  ends within 30 seconds, but nothing of theirs is deleted; enabling them restores everything. An administrator
  cannot demote, disable or delete themselves, so they cannot lock themselves out. `flare user role`, `disable`
  and `enable` do the same from the command line, and `flare user list` shows who is disabled.
- The Admin UI (`/admin`): a web page built into the server, where an administrator signs in and adds colour
  themes (a colour picker for each of the 32 colours, a live preview, a check that every text is readable, and
  import, export, duplicate and delete), changes the runtime settings (each saved on its own, reset to the
  configuration file's value, with a mark and a banner for changes that wait for a restart), manages users
  (add, role, password, disable and enable, delete, and see and revoke tokens) and edits access rules. It is plain
  HTML, CSS and JavaScript with no build step, served under a strict content security policy, and does nothing
  the `/v1/admin` API does not check. `make admin-test` tests its colour maths and `make admin-e2e` drives it in a
  real browser against the real server; the server workflow runs both and can now be started by hand.

