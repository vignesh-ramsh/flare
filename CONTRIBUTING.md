# Contributing to Flare

Thanks for helping! Flare is at an early stage: the design is written down in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and the code is about to start. Feedback on the design is
as welcome as code.

## Ways to contribute

- Report bugs and suggest features through [issues](https://github.com/vignesh-ramsh/flare/issues).
- Discuss the design: open a proposal issue that names the section of `docs/ARCHITECTURE.md` it is about.
- Send pull requests: code, tests, documentation, translations.
- Help other people in issues.

## Before you start

- For anything bigger than a small fix, open an issue first so we can agree on the approach before you
  put in the work.
- **Security issues:** don't open a public issue; follow [SECURITY.md](SECURITY.md).
- Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## Development setup

The server (Go) and the apps (Flutter) don't exist yet. This section will list the exact setup steps
once their skeletons land. The planned toolchain is the latest stable Go, the stable Flutter channel and
`make` (see [docs/ARCHITECTURE.md §11](docs/ARCHITECTURE.md#11-recommended-tech-stack)).

## Pull requests

- Keep each pull request to one change; small ones get reviewed faster.
- Add or update tests for any change in behaviour.
- Update the docs when you change behaviour, configuration or the API. `api/openapi.yaml` is the source
  of truth for the API.
- Keep the apps brand-neutral: never write "Flare" in app UI code or translation strings. Use
  `Brand.name` in code and `{appName}` in ARB strings, so rebranded builds stay correct
  ([§12.6](docs/ARCHITECTURE.md#126-keeping-rebranding-working)).
- Fill in the pull request template.

### Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/), for example
`feat(server): add SSE keepalive` or `fix(app): reconnect after network change`.
Scopes: `server`, `relay`, `cli`, `app`, `web`, `brand`, `deploy`, `ci`, `docs`.

### Sign off your commits (DCO)

Every commit needs a `Signed-off-by` line. It certifies the
[Developer Certificate of Origin](https://developercertificate.org/): that you wrote the change, or
otherwise have the right to submit it under the project's licence. Use your real name and the same
email address as the commit author.

```bash
git commit -s -m "fix(server): close idle WebSocket after timeout"
```

Forgot? Add the sign-off to your last commit with `git commit --amend -s --no-edit`, or to every commit
on your branch with `git rebase --signoff main`, then force-push your branch.

## Licensing of contributions

Flare is licensed under the [Apache License 2.0](LICENSE). Under section 5 of the licence, anything you
intentionally submit is licensed under the same terms, so there is no separate contributor licence
agreement.

New source files start with a copyright line and an SPDX licence identifier, written in that
language's comment syntax:

```go
// Copyright 2026 The Flare Authors
// SPDX-License-Identifier: Apache-2.0
```

"The Flare Authors" means everyone who has contributed, as recorded in the git history. Use the year
the file was created.

- Don't copy in code you didn't write unless its licence is compatible with Apache-2.0, keep its
  copyright notices, and mention it in the pull request.
- New dependencies must use a licence compatible with Apache-2.0 distribution, such as MIT, BSD, ISC,
  Apache-2.0, or MPL-2.0 used unmodified. GPL, LGPL and AGPL dependencies can't be accepted.
