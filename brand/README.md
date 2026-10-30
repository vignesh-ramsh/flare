# Brand folders

A **brand folder** holds everything that makes an app build yours: its name, logo, colours,
identifiers and default server. Build the apps with a different brand folder and you get a fully
rebranded app, with no source file edited.

This folder is the official Flare brand. [`examples/acme`](examples/acme) is a complete example you can
copy.

## Make your own brand

You need the repository and the Dart SDK (it comes with Flutter).

```bash
mkdir ../my-brand
cp brand/brand.yaml ../my-brand/
cp /path/to/your/icon.png /path/to/your/symbol.png ../my-brand/
```

Edit `../my-brand/brand.yaml`: at least the name, the identifiers and the colours. Then generate the
app's branding files from it:

```bash
make brand BRAND_DIR=../my-brand
```

If something is wrong, nothing is generated, and every problem is listed with the field it is about:

```
error: app.url_scheme: "https" belongs to the operating system or browsers; pick your own
error: ids.android: must look like com.company.app: at least two parts, ... (found "myapp")
error: look.symbol: symbol.png has no transparent background. Notification and tray icons use only the shape ...
```

Before you publish a build, run it again in release mode. It refuses placeholder identifiers and
addresses, and a missing privacy policy, which the app stores require:

```bash
make brand BRAND_DIR=../my-brand RELEASE=1
```

Keep your brand folder in its own (even private) repository. It is the only thing a rebrand owns, and
updating to a new Flare release is then just checking out the new tag and running the same commands.

## The files

| File | What it is |
|---|---|
| `brand.yaml` | Names, identifiers, colours, default server and links (reference below) |
| `icon.png` | The app icon artwork: a square PNG of at least 1024×1024 pixels |
| `symbol.png` | A one-colour silhouette for notification and tray icons: a square PNG of at least 256×256 pixels |
| `firebase/google-services.json` | Optional: from your Firebase project, for push notifications on Android |
| `source/` | Optional: editable SVG sources of the two images. The generator ignores it |

### icon.png

The logo on a **transparent background**, or a full-bleed opaque image. Keep everything that matters
inside the **central two thirds**: Android crops its adaptive icons to a circle or a rounded square, and
you get a warning if the artwork reaches beyond that. The generator puts the artwork on
`look.icon_background` wherever a platform needs an opaque tile (iOS, macOS, Windows, Linux).

### symbol.png

Only the **shape** is used, never the colours. It must have a **transparent background**: a solid image
would show up as a plain square in notifications, so the generator refuses it. The generator trims it
to the shape and adds the padding each platform needs, and draws it in the colour each platform wants
(white on Android and Linux, black "template" on the macOS menu bar).

## brand.yaml

Values in quotes must stay quoted, in particular colours (`"#F97316"`, because `#` starts a comment in
YAML) and the Apple Team ID.

| Field | Meaning and rules |
|---|---|
| `schema` | Always `1` |
| **app** | |
| `name` | Shown in the launcher, window title, notifications and About screen. At most 30 characters: letters, digits, spaces and `. , ' & + ! _ -` |
| `short_name` | Where space is tight (the Android launcher). At most 12 characters, same characters. Optional if `name` is that short |
| `description` | One line, at most 80 characters, without `\ " $ ;` |
| `url_scheme` | Subscribe links look like `<scheme>://subscribe/<host>/<topic>`. Lowercase, 2–32 characters. Not a scheme that belongs to something else (`http`, `file`, `mailto`, …) |
| `app_links_domain` | Optional host for Universal Links and Android App Links, like `links.example.org` |
| **ids** | These identify your app to the app stores and to push services. Each must be unique to you |
| `android` | Android `applicationId`, like `com.company.app` |
| `apple` | iOS and macOS bundle ID, like `com.company.app`. The iOS notification extension becomes `<id>.NotificationService`, and the App Group `group.<id>` |
| `apple_team_id` | The 10 characters of your Apple Developer Team ID, or `""` |
| `windows` | MSIX package identity: 3–50 letters, digits, `.` or `-`, like `Company.App` |
| `windows_publisher` | The subject of your code-signing certificate, like `"CN=Company"` |
| `linux` | Reverse-DNS ID for the `.desktop` file and AppStream, like `com.company.App`. Its last part, in lower case, is the program name |
| **look** | |
| `icon`, `symbol` | File names, relative to the brand folder |
| `icon_background` | Colour behind the artwork, `"#RRGGBB"` |
| `primary_color` | The app's main colour, `"#RRGGBB"` |
| **defaults** | |
| `server_url` | Server pre-filled for new users. `https://` (plain `http://` only for localhost), no query. `""` makes users enter one |
| `allow_custom_servers` | `false` locks the app to `server_url`, for company builds. Then `server_url` must be set |
| `push_relay_url` | The push relay your build registers with, or `""` if your servers push directly (see [docs/ARCHITECTURE.md §5.3](../docs/ARCHITECTURE.md#53-push-credentials-and-the-push-relay)) |
| **links** | Shown in Settings and About, and in store listings. `https://` or `""` |
| `website`, `support`, `source_code` | Optional |
| `privacy_policy` | Required by the app stores; release mode refuses `""` |
| **firebase** | |
| `android_config` | Path to your `google-services.json`, or `""` for no FCM push on Android. It must be the file for the app registered under `ids.android` |

## What gets generated

`make brand` writes into the `app/` folder, and only into paths the generator owns (they are
git-ignored). It empties those first, so switching brands never leaves the old brand's files behind.

- `lib/brand/brand.g.dart`: the `Brand` class the app's Dart code reads, so the code never contains
  the name itself.
- `assets/brand/`: the in-app logo and the system-tray icons.
- **Android:** `android/brand.properties` and `android/app/src/brand/res/` (launcher, adaptive,
  monochrome and notification icons, colours), plus `google-services.json` if you have one.
- **iOS and macOS:** `Brand.xcconfig` and the `AppIcon.appiconset` icon sets.
- **Windows:** `brand.cmake` and `app_icon.ico`.
- **Linux:** `brand.cmake`, the `.desktop` file, AppStream metadata and icons.
- **Store listings:** the title, short description and links in `fastlane/metadata/`.

Secrets are never part of a brand folder: signing keys, certificates, APNs keys and FCM service
accounts belong in your CI secrets or on your server. See
[docs/ARCHITECTURE.md §12.4](../docs/ARCHITECTURE.md#124-identity-that-must-be-yours).

## Licence and trademark

The code is Apache-2.0. The **Flare name and logo are not** (see [TRADEMARKS.md](../TRADEMARKS.md)): if
you publish your own build, replace the name, the logo and the identifiers with your own, which is what
a brand folder is for. The example brand in `examples/` *is* Apache-2.0 licensed, so you can start from
it freely. Every build keeps a "Based on Flare" line in its About screen.
