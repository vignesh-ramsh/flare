# Flare

Flare is an open-source, self-hostable pub/sub notification and messaging service. Send a message to a
topic with a plain HTTP request, and every device subscribed to that topic gets it: Android, iOS, macOS,
Windows, Linux and the web.

```bash
curl -d "Backup finished" https://flare.example.com/backups
```

- **Self-host it:** one static binary, SQLite by default, Docker Compose in a few minutes. See
  [server/README.md](server/README.md). It is early: it is open by default, so turn on accounts
  (`auth.default-access: deny-all`) before exposing it. Push notifications work through Web Push,
  UnifiedPush, FCM and APNs; the relay for the official apps, and rate limits, are still to come.
- **Rebrand it:** set your own app name, logo, colours and identifiers in one brand folder and rebuild
  the apps. No source changes needed. See [brand/README.md](brand/README.md).

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the architecture and tech stack.

## Contributing

Contributions are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Everyone taking part follows the
[Code of Conduct](CODE_OF_CONDUCT.md). Please report security issues privately as described in
[SECURITY.md](SECURITY.md).

## License

Flare is licensed under the [Apache License, Version 2.0](LICENSE). The Flare name and logo are not
covered by that licence; see [TRADEMARKS.md](TRADEMARKS.md).
