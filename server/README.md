# Flare server

The Flare server and its command-line client: one static binary, `flare`. Design and
reasoning: [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md). The HTTP contract:
[api/openapi.yaml](../api/openapi.yaml).

> **Status: early.** Publishing, subscribing, accounts and push notifications work, with the delivery guarantee
> described below. Out of the box the server is open: anyone who can reach it can publish and
> read every topic. Set `auth.default-access: deny-all` and create users (see
> [Accounts and access](#accounts-and-access)) before putting it on the internet, and read
> [What is and isn't there yet](#what-is-and-isnt-there-yet).

## Try it

You need Go 1.25 or newer.

```bash
make server                 # builds server/bin/flare (a static binary, about 14 MiB)
./server/bin/flare serve    # listens on :8080, keeps messages in ./flare.db
```

In another terminal:

```bash
# publish: anything that can make an HTTP request can
curl -H "Title: Backup failed" -H "Priority: high" -H "Tags: warning" \
     -d "rsync exited with code 23" localhost:8080/backups

# subscribe: one JSON object per line, as messages arrive
curl -s localhost:8080/backups/json

# ...or catch up on what you missed, then stop
curl -s "localhost:8080/backups/json?poll=1&since=1h"
```

Or with the bundled client:

```bash
./server/bin/flare publish -p high backups "Backup finished"
echo "disk is full" | ./server/bin/flare publish --tags warning alerts
./server/bin/flare subscribe backups alerts
```

## Publishing

`POST` or `PUT` to `/{topic}`. The body is the message. Settings go in headers, or in query
parameters of the same name in lower case:

| Header | Meaning |
|---|---|
| `Title` | A one-line title, up to 250 characters |
| `Priority` | 1-5, or `min`, `low`, `default`, `high`, `urgent` |
| `Tags` | Up to 20, comma-separated |
| `Click` | A URL to open when the notification is tapped |

Non-ASCII header values can be RFC 2047 encoded (`=?UTF-8?B?...?=`). You can also publish
JSON to `POST /v1/messages`. Topic names are 1-64 letters, digits, `-` or `_`; `v1`, `healthz`,
`readyz` and `metrics` are reserved.

Settings the server doesn't support yet (`Delay`, `Actions`, `Attach`, `Filename`, `Icon`,
`Markdown`, `Cache`) are **refused with an error**, never silently ignored: dropping `Delay`
would send at once a message the publisher wanted held back.

## Subscribing

| Endpoint | For |
|---|---|
| `GET /{topics}/json` | Scripts and `curl`: newline-delimited JSON |
| `GET /{topics}/sse` | Browsers' `EventSource` |
| `GET /v1/ws` | Apps: one WebSocket for many topics, to subscribe and publish |

`{topics}` is one topic or several separated by commas. `since` says where to start: `all`,
a message ID, a duration such as `10m`, or a unix timestamp. Without it you get new messages
only.

## How delivery works

- A message is **stored before the publisher is answered**, and kept for `cache-duration`
  (24 hours by default). Messages have IDs that sort by time.
- A subscriber that disconnects remembers the ID of the last message it got and reconnects
  with `since=<that ID>`. It receives everything it missed, **each message once, in order**,
  even if it reconnects while others are publishing.
- A subscriber that can't keep up is disconnected rather than allowed to hold up the others
  or use unlimited memory. It reconnects the same way and loses nothing.

`flare subscribe` does all of this for you, and its source,
[internal/client](internal/client/client.go), is the reference for any other client.

The rule that makes it work is in [internal/realtime/hub.go](internal/realtime/hub.go):
publishing assigns the ID, stores the message and hands it to subscribers in one step, so ID
order, storage order and delivery order are the same. The tests publish from many goroutines
while subscribers connect at random moments, and check that nothing is missed or repeated.

## Accounts and access

By default the server is open, which is convenient to try it out. To restrict it, set
`auth.default-access: deny-all` (in the config file, or `FLARE_AUTH_DEFAULT_ACCESS`), then say who may
do what. These commands work on the database directly, so run them on the server's machine (in Docker,
`docker compose -f deploy/docker-compose.yml exec flare flare ...`) and they take effect at once on a
running server:

```bash
flare user add --admin alice             # asks for a password; an admin may do everything
flare user add bob
flare access grant bob 'alerts-*' read-write
flare access grant everyone status read-only      # no login needed to read "status"
flare token add --label backup-server bob         # prints a token, once, for a script
flare access list
```

Passwords are read from the terminal, or from standard input if it is piped, never from the command
line. Rules say what a user (or `everyone`) may do on the topics matching a pattern, where `*` stands
for any characters: `read-write`, `read-only`, `write-only` or `deny-all`. Of the rules that match a
topic, the most specific wins: an exact name beats a pattern, a longer pattern beats a shorter one, a
user's own rule beats the one for `everyone`, and at equal specificity a `deny-all` beats an allow.
Where no rule matches, `auth.default-access` applies. Rules work in both directions: under a
`read-write` default they close off some topics, under a `deny-all` default they open some.

Using it:

```bash
flare login -s https://flare.example.com alice     # asks for the password; prints a token
export FLARE_TOKEN=flr_...                         # or --token on publish and subscribe
flare publish -s https://flare.example.com alerts-db "Disk almost full"

curl -H "Authorization: Bearer $FLARE_TOKEN" -d "Disk almost full" https://flare.example.com/alerts-db
curl -u alice:password -d "hello" https://flare.example.com/alerts-db     # fine for curl; apps use tokens
```

- **Tokens** (`flr_` and 43 characters) can be revoked one at a time (`flare token delete <id>`, or
  `DELETE /v1/account/tokens/{id}`) and may expire (`--expires 30d`). Only a hash is stored, so a token
  can't be shown again. A token can't be used to create more tokens: that needs the password, so a
  leaked token can be revoked for good.
- **A refusal tells you what to do.** Not logged in: `401`, so a client knows to ask for a login.
  Logged in but not allowed: `403`. Credentials that are wrong are refused as such, never quietly
  treated as an anonymous visitor's.
- **Revoking cuts off open connections.** A deleted token, a deleted user, a changed password (for
  connections that logged in with it) or a rule that takes away read access ends the streams and
  WebSockets that depended on it within 30 seconds. Tokens keep working after a password change; revoke
  them separately.
- **Guessing passwords is slow.** Checking a password takes memory (Argon2id), so the server does at
  most four at a time, and a client that fails to log in 10 times in 10 minutes is told to wait (`429`).
  Behind a reverse proxy, list it in `trusted-proxies` so that clients are told apart; otherwise
  every client looks like the proxy.
- **Browsers and WebSockets.** A WebSocket can't carry a header from a browser, so the first message
  on it may be `{"op":"auth","token":"flr_..."}`. Tokens never go in URLs.

The full contract is in [api/openapi.yaml](../api/openapi.yaml).

## Push notifications

An app that is not running can be woken by the push service of its platform. The server supports four:

| Service | For | Needs |
|---|---|---|
| **Web Push** | Browsers, and UnifiedPush distributors that give keys | Nothing: the server makes its own signing key (VAPID) and keeps it in the database |
| **UnifiedPush** | De-Googled Android, through a distributor such as ntfy | Nothing |
| **FCM** | Android | The app publisher's Firebase service account key, in `push.apps` |
| **APNs** | iOS and macOS | The app publisher's Apple `.p8` key, in `push.apps` |

FCM and APNs credentials belong to one app's publisher, so a server can push to the apps it holds keys
for (your own branded app). The official apps will use a relay, which is not built yet.

How an app uses it (the contract is in [api/openapi.yaml](../api/openapi.yaml)):

```bash
# 1. Register the device. The secret is shown once; keep it. Log in first (Authorization header)
#    so that the device follows what you may read.
curl -H "Authorization: Bearer $TOKEN" -d '{"app_id":"org.example.flare","platform":"android",
  "provider":"unifiedpush","endpoint":"https://ntfy.example.com/upabc123"}' https://flare.example.com/v1/devices
# {"id":"ZkQ...","secret":"flrd_..."}

# 2. Say which topics to be woken for. The whole list, each time.
curl -X PUT -H "Device-Secret: flrd_..." -d '{"topics":["alerts-db","backups"]}' \
  https://flare.example.com/v1/devices/ZkQ.../subscriptions
# {"subscriptions":[{"topic":"alerts-db","id":"Hx1..."},{"topic":"backups","id":"q7P..."}]}
```

From then on, publishing to `alerts-db` sends the device a notification: one JSON object, either the message
(the same fields as on a stream, with `"sub"` set to the subscription's ID) or, if it is too big for the push
service or `push.content-free` is on, `{"event":"poll_request","sub":"...","id":"..."}`, with no topic and no
content. The app then fetches the message with `GET /v1/messages/{id}`, using its own credentials. It knows
the topic from `sub`, which it saved when it subscribed. Apps drop duplicates by message `id`.

- **Delivery.** Notifications are queued in the same transaction that stores the message, so a stored message is
  never left without them and a restart loses none. A failed send is retried with growing delays (10 s to 30
  min, honouring the push service's `Retry-After`) until the message expires. A push service that says the
  device is gone (`410 Unregistered`, FCM `UNREGISTERED`) gets the device forgotten.
- **Access.** A device acts with the access of whoever registered it, checked when it follows a topic and again
  before every notification, so taking access away stops its notifications. A device registered without
  logging in has what an anonymous visitor has; on a server that lets visitors in only after login, devices
  must belong to a user.
- **What the push services see.** Web Push is encrypted for the device. FCM, APNs and a UnifiedPush distributor
  can read what they carry. If that matters, set `push.content-free: true`: they then see only that something
  arrived.
- **Endpoints.** Web Push and UnifiedPush endpoints are URLs that users choose, so the server accepts only https
  URLs at public addresses, checked at the moment it connects, and follows no redirects. For a distributor on
  your own network, set `push.allow-private-endpoints: true`, which also lets anyone who can register a
  device make the server send requests to that network.
- **Limits.** 20 devices per user (1000 anonymous ones in all), 100 topics per device. Registering a token
  that is already registered replaces the earlier registration.

**Not yet:** the relay, so FCM and APNs work only for apps whose keys are in `push.apps`; per-topic privacy
settings; rate limits on registration. **Tested against fakes only:** FCM and APNs. The Web Push encryption
matches the test vector in RFC 8291; the FCM and APNs requests follow Google's and Apple's documentation, but
have not been run against the real services, so expect to fix details when you first do.

## Configuration

Settings come from built-in defaults, then a YAML file (`--config` or `FLARE_CONFIG`), then
`FLARE_*` environment variables, then flags. A setting that is misspelled, or not supported
yet, is an **error**, so a typo can never leave something silently unconfigured.
[deploy/server.yml](../deploy/server.yml) shows them all.

| Setting | Default | Meaning |
|---|---|---|
| `base-url` | | The public address, for links and `/v1/config` |
| `listen` | `:8080` | Where to listen |
| `database` | `sqlite://flare.db` | `sqlite://<path>` (`sqlite:///var/lib/flare/flare.db` for an absolute path) |
| `cache-duration` | `24h` | How long messages are kept (1m to 8760h) |
| `message-size-limit` | `4096` | Largest message, in bytes |
| `auth.default-access` | `read-write` | What a visitor who is not logged in may do, and what a user may do where no rule applies: `read-write`, `read-only`, `write-only`, `deny-all` |
| `push.enabled` | `true` | Accept devices and send push notifications |
| `push.content-free` | `false` | Never put message content in a push; devices fetch it |
| `push.web-push`, `push.unified-push` | `true` | Those services |
| `push.vapid-subject` | `base-url` | `mailto:` or `https:` contact for Web Push services |
| `push.allow-private-endpoints` | `false` | Let push endpoints be on private networks and plain http |
| `push.apps` | none | Per-app FCM and APNs credentials; see [deploy/server.yml](../deploy/server.yml) |
| `trusted-proxies` | none | Reverse proxies (addresses or CIDR ranges) believed when they say who a client is in `X-Forwarded-For`; used to count failed logins per client |
| `branding.name`, `.primary-color`, `.app-url-scheme` | Flare, `#F97316`, `flare` | How the server presents itself to apps |

Logs are JSON on stderr (`--log-format text` for people). They record each request's route
and status, and never topic names, message contents or client addresses.

## Running it for real

Use the container image and Caddy for automatic HTTPS: see [deploy/](../deploy/)
(`docker compose -f deploy/docker-compose.yml up -d`). The server speaks plain HTTP and
expects a reverse proxy for TLS. The proxy must not buffer responses, and must pass
WebSocket upgrades; the Caddyfile there does both, and
[docs/ARCHITECTURE.md §13.2](../docs/ARCHITECTURE.md#132-other-ways-to-install) has the
nginx equivalent.

Shutting down (`SIGTERM` or Ctrl-C) ends open streams at once, lets requests in progress
finish, and exits. Subscribers reconnect and catch up.

## What is and isn't there yet

| | |
|---|---|
| **Works** | Publishing (headers and JSON); subscribing by JSON stream, SSE, WebSocket and poll; catching up with `since`; SQLite storage that survives restarts; expiry; users, passwords, access tokens and per-topic access rules, with the revocation of open connections and a limit on failed logins; push notifications through Web Push, UnifiedPush, FCM and APNs, with a durable queue and retries; health checks; `/v1/config`; graceful shutdown; the `flare publish`, `flare subscribe`, `flare login`, `flare user`, `flare token` and `flare access` commands |
| **Not yet** | Rate limits on publishing and other abuse protection (the failed-login limit is the only one). Sign-up by users themselves, changing one's own password, reserving topic names. The push relay. Attachments, delayed delivery, action buttons, Markdown. Retracting a message. End-to-end encryption. PostgreSQL and NATS (cluster mode). Prometheus metrics. The web app. Built-in TLS |

Until rate limits exist, **don't expose an open server to the internet**: set
`auth.default-access: deny-all`, or listen on `127.0.0.1`, or keep it on a private network. Even with
accounts, anyone who can log in can publish as fast as the server lets them. The server warns at
start-up when it is open and reachable from outside. Browsers may only open a WebSocket from the
server's own origin; other clients can open one from anywhere.

## Code layout

```
cmd/flare/          the command: serve, publish, subscribe, login, user, token, access
internal/
  api/              HTTP routes, publish and subscribe handlers, middleware
  realtime/         the hub (storing and delivering) and the wire formats: JSON, SSE, WebSocket
  push/             devices, the notification queue and its dispatcher, and the senders (Web Push, UnifiedPush, FCM, APNs)
  broker/           fan-out to live subscribers (in-process; NATS later)
  store/            message storage (SQLite; PostgreSQL later)
  message/          what a message is, and the rules for what may be published
  id/               time-ordered message IDs (UUIDv7)
  auth/             users, passwords, tokens, and who may read and write which topic
  config/           settings: file, environment, validation
  client/           publish and follow, with reconnect and resume
  server/           wiring, background purge, graceful shutdown
  apierr/           error codes shared by HTTP and WebSocket
migrations/         the database schema, compiled into the binary
```

## Development

```bash
make server-test    # gofmt check, go vet, and all tests with the race detector
make api-lint       # validate api/openapi.yaml (needs Node.js)
```

Go 1.25 or newer. CI runs the tests on the minimum Go version (the one in `go.mod`) and on
the latest. A test fails if the routes and `api/openapi.yaml` disagree, so change both together.
Tests use real SQLite files and real HTTP servers rather than mocks.

Run `go mod tidy` after changing dependencies; CI checks that `go.mod` and `go.sum` are tidy.
