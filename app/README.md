# The app

The Flutter app for Android, iOS, Windows, macOS and Linux (the web app comes later). It is a skeleton:
the layouts, the on-device database, the topic list and the connection to a server work, so it receives and
keeps messages for the topics you subscribe to; sign-in, notifications and sending are still to come.
What it shows and why is in [docs/APP-DESIGN.md](../docs/APP-DESIGN.md).

## Set up and run

You need Flutter 3.47 or newer (Dart 3.13). From the repository root:

```bash
make app-setup
```

That writes the three kinds of file the app needs and git does not keep: the brand files (`make brand`),
the translations (Flutter's `gen-l10n`) and the database code (`build_runner`). Then:

```bash
cd app && flutter run -d linux
```

On Linux you also need `clang cmake ninja-build pkg-config libgtk-3-dev liblzma-dev`.

To check your change the way CI does, run `make app-check`: formatting, analysis, the brand-name check and
all the tests. Some tests run the app's connection code against the real Flare server; they are skipped
unless you build it and say where it is:

```bash
make server
FLARE_SERVER_BIN=$PWD/server/bin/flare make app-check
```

## Where things are

| Folder | What is there |
|---|---|
| `lib/app` | The app widget, theme, routes, window size classes and the adaptive shell (bottom bar, rail, sidebar) |
| `lib/features` | One folder per screen: `topics`, `thread`, `add` (Subscribe), `inbox`, `settings` |
| `lib/net` | The connection: protocol frames, backoff, one WebSocket per server, and the manager that follows the database |
| `lib/data` | Repositories over the database, the models screens read, and the Riverpod providers |
| `lib/core/db` | The drift schema and how the database is opened |
| `lib/ui` | Widgets several screens share: message card, priority badge, unread badge, logo, empty state |
| `lib/brand` | `brand.g.dart`, made by `make brand`. The only place the project name may appear |
| `l10n` | The translations, as `.arb` files. Write the app's name as `{appName}`, never as text |
| `tool/brand` | The brand generator and its tests (`make brand-test`) |

## Rules the code follows

- **Nothing in `lib` or `l10n` says the project's name.** Use `Brand.name`, and `{appName}` in translations.
  `make brand-check` fails the build otherwise, so a rebranded app never shows the original name.
- **Screens read only the local database.** The network will write into it; a screen never waits on one.
- **The layout follows the window's width, not the device.** `WindowClass` has the four classes; the same
  route is a page, a pane, a sheet or a dialog depending on it.
- **Every string comes from `l10n/app_en.arb`.**

## What works on which platform

CI (`.github/workflows/app.yml`) builds the app for each platform from a clean checkout and then checks
the result: the program, bundle or package carries the name and identifier from the brand files. Only Linux
has also been run (here, against the real server). Nothing has been run on a phone, a simulator, an
emulator, a Mac or a Windows PC.

| | Builds in CI | Named and identified from the brand folder | Run |
|---|---|---|---|
| Linux | Yes | Yes: binary name, application ID and window title (`linux/brand.cmake`) | Yes, against the real server |
| Android | Yes (debug APK) | Yes: package name and label checked in the APK | No |
| iOS | Yes (simulator build) | Yes: bundle ID and display name checked in the app's Info.plist | No |
| macOS | Yes (debug) | Yes: bundle ID and name checked, and the sandbox's network entitlement | No |
| Windows | Yes (debug) | Yes: executable name and version resource checked | No |
| Web | The project is not created yet | | |

Not built in CI yet: release builds and signing, the Android `play` and `foss` flavours, and any packaging
(MSIX, dmg, Flatpak, App Store, Play Store). The URL scheme is defined for Android, but no platform registers
it yet; that comes with deep links.

## The database

Messages, subscriptions and the cursors that say where each topic's stream resumes live in `messages.sqlite`
in the platform's application-support folder (on Linux, `~/.local/share/<application id>/`). Tokens and
device secrets will not: they go to the platform's secure storage.

## If the SQLite download is blocked

The `sqlite3` package fetches a checked, prebuilt SQLite from GitHub when the app is built. Where GitHub is
not reachable, tell it to use the system's SQLite for your own builds by adding this to `app/pubspec.yaml`
(do not commit it; release builds should bundle their own copy):

```yaml
hooks:
  user_defines:
    sqlite3:
      source: system
```
