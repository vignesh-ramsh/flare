# Brand tool

Validates a [brand folder](../../../brand/README.md) and generates the per-platform branding files (names,
identifiers, icons) that the app is built from. Design: [docs/ARCHITECTURE.md §12](../../../docs/ARCHITECTURE.md#12-white-label-builds-custom-app-name-and-logo).

Most people run it through `make` from the repository root:

```bash
make brand                                  # the official brand
make brand BRAND_DIR=../my-brand            # your own
make brand BRAND_DIR=../my-brand RELEASE=1  # release mode: refuse placeholders
make brand-test                             # analyze and test this tool
```

Or directly, from this folder:

```bash
dart pub get
dart run bin/brand.dart --brand ../../../brand --app ../..
```

Options: `--release`, and `--upstream <folder>` for the project's own brand, which defaults to
`<app>/../brand` and is used to spot builds that reuse its identity. Exit status: `0` generated,
`64` bad command line, `65` the brand folder has problems (nothing is generated).

Needs Dart 3.9 or newer; tested on 3.9 and the current stable. It has no Flutter dependency, so it works
before the Flutter app exists.

## How it works

```
brand folder ──▶ loader ──▶ Brand ──▶ emitters ──▶ Outputs (in memory) ──▶ writeOutputs ──▶ app/
                 spec.dart   (spec +    emit/*.dart  path → bytes            generator.dart
                 loader.dart  images)   icons.dart
```

| File | Does |
|---|---|
| `lib/src/spec.dart` | Parses `brand.yaml` and validates every field, collecting all problems |
| `lib/src/loader.dart` | Loads the images and `google-services.json`, and applies the release checks |
| `lib/src/raster.dart` | A small RGBA image type: resizing (premultiplied, so no fringes), compositing, masks |
| `lib/src/image_io.dart` | PNG decoding (via `package:image`), and our own deterministic PNG and ICO writers |
| `lib/src/icons.dart` | One recipe per platform for turning the two source images into icons |
| `lib/src/emit/*.dart` | One emitter per platform: which files, with which contents |
| `lib/src/generator.dart` | Renders everything in memory, then writes it, owning a fixed set of paths |

Output is deterministic: the same brand always produces byte-identical files, which keeps builds
reproducible. We write PNGs ourselves for that reason, and to control the colour type (iOS icons must
have no alpha channel).

## Tests

```bash
dart test
```

They cover the validation rules, the image code, every generated file (sizes, formats, contents), that
Dart output passes the analyzer, that switching brands leaves nothing behind, that unrelated files are
never touched, and that the example brand's output contains no trace of the Flare name.

## Adding or changing a generated file

1. Add it to an emitter in `lib/src/emit/`.
2. Add its path to `ownedFiles` or `ownedDirectories` in `lib/src/generator.dart`. `writeOutputs`
   refuses to write anything not listed, and deletes listed files a brand doesn't produce.
3. Add it to the generated-files list in the root `.gitignore` (a test fails if you forget).
4. Add a test in `test/generator_test.dart`.
5. Update the table in [docs/ARCHITECTURE.md §12.3](../../../docs/ARCHITECTURE.md#123-how-a-branded-build-works).

Generated code and files must never contain the project name; take it from the brand
(`s.name` in an emitter). Only the attribution constants in `emit/dart.dart` may.

## Dependencies

`pubspec.lock` is not committed: this tool must work on every Dart version from its minimum up, and a
lockfile resolved on a new SDK can pin packages that an older one can't use. Constraints name versions
that have been tested on the minimum and on the current stable SDK.
