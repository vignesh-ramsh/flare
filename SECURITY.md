# Security Policy

## Reporting a vulnerability

Please **don't** report security issues in public issues or pull requests.

Report them privately through GitHub instead: open the repository's **Security** tab and choose
**Report a vulnerability**, or go straight to
<https://github.com/vignesh-ramsh/flare/security/advisories/new>.

Please include:

- the affected component (server, push relay, CLI, the app on which platform, web app, brand generator,
  build workflows) and the version or commit;
- steps to reproduce, or a proof of concept;
- what an attacker could do with it;
- a suggested fix, if you have one.

## What to expect

- We acknowledge your report within 7 days.
- We keep you updated while we investigate and fix the issue, and agree a disclosure date with you. By
  default, we aim to fix and disclose within 90 days of the report, or sooner once a fix is released.
- We credit you in the security advisory and the release notes, unless you'd rather stay anonymous.

## Supported versions

Flare has no releases yet. Once it does, security fixes go into the latest minor release of the current
major version.

## Scope

**In scope:** the code in this repository (server, push relay, CLI, apps, web app, brand generator and
build workflows) and the official builds and services the project runs.

**Out of scope:**

- Rebranded builds and servers run by other people. Report those to whoever publishes or runs them,
  unless the problem is in the upstream code they are built from.
- Vulnerabilities in dependencies that are already public. Report them upstream, and tell us if Flare
  needs to update.
- Attacks that require an already compromised device or server administrator access.
- Volumetric denial of service. Rate limits are configurable by the operator.
