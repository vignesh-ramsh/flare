# Flare

Flare is an open-source, self-hostable pub/sub notification and messaging service. Send a message to a
topic with a plain HTTP request, and every device subscribed to that topic gets it: Android, iOS, macOS,
Windows, Linux and the web.

```bash
curl -d "Backup finished" https://flare.example.com/backups
```

- **Self-host it:** one binary, SQLite by default, Docker Compose in a few minutes.
- **Rebrand it:** set your own app name, logo, colours and identifiers in one brand folder and rebuild
  the apps. No source changes needed.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the architecture and tech stack.
