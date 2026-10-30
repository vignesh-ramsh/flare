# Flare

Flare is a pub/sub notification and messaging service. Send a message to a topic with a plain HTTP
request, and every device subscribed to that topic gets it: Android, iOS, macOS, Windows, Linux and the web.

```bash
curl -d "Backup finished" https://flare.example.com/backups
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the architecture and tech stack.
