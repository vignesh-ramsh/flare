# Flare server

The Flare server and its command-line client: one static binary, `flare`. Design and
reasoning: [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md). The HTTP contract:
[api/openapi.yaml](../api/openapi.yaml).

> **Status: early.** Publishing and subscribing work, with the delivery guarantee described
> below. There are **no accounts yet**, so by default anyone who can reach the server can
> publish and read every topic. See [What is and isn't there yet](#what-is-and-isnt-there-yet)
> before putting it on the internet.

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
| `auth.default-access` | `read-write` | What anyone may do: `read-write`, `read-only`, `write-only`, `deny-all` |
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
| **Works** | Publishing (headers and JSON); subscribing by JSON stream, SSE, WebSocket and poll; catching up with `since`; SQLite storage that survives restarts; expiry; a server-wide access policy; health checks; `/v1/config`; graceful shutdown; the `flare publish` and `flare subscribe` client |
| **Not yet** | Accounts, access tokens and per-topic rules. Rate limits and abuse protection. Push notifications through FCM, APNs, UnifiedPush and Web Push, and the push relay. Attachments, delayed delivery, action buttons, Markdown. Retracting a message. End-to-end encryption. PostgreSQL and NATS (cluster mode). Prometheus metrics. The web app. Built-in TLS |

Until accounts and rate limits exist, **don't expose the server to the internet unprotected**.
Listen on `127.0.0.1`, keep it on a private network, or put it behind a proxy that
restricts who can connect. The server warns at start-up when it is open and reachable from
outside. For the same reason, anyone can open a WebSocket from a non-browser client;
browsers may only connect from the server's own origin.

## Code layout

```
cmd/flare/          the command: serve, publish, subscribe
internal/
  api/              HTTP routes, publish and subscribe handlers, middleware
  realtime/         the hub (storing and delivering) and the wire formats: JSON, SSE, WebSocket
  broker/           fan-out to live subscribers (in-process; NATS later)
  store/            message storage (SQLite; PostgreSQL later)
  message/          what a message is, and the rules for what may be published
  id/               time-ordered message IDs (UUIDv7)
  auth/             who may read and write which topic
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
