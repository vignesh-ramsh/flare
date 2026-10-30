# Trademark Policy

Flare's code is open source under the [Apache License 2.0](LICENSE). The **Flare name and logo are
not**: section 6 of the licence grants no rights to use them. This policy explains when you can use
them, and when you need your own branding.

**In short: use the code however you like, but if you publish your own build of the apps, give it your
own name and logo.** The brand folder makes that a few minutes of work (see
[docs/ARCHITECTURE.md §12](docs/ARCHITECTURE.md#12-white-label-builds-custom-app-name-and-logo)).

## What this policy covers

- The name "Flare" when used for notification or messaging software or services.
- The Flare logos and app icons in `brand/`. The example brand in `brand/examples/` is
  Apache-2.0 licensed and free to use as a starting point.

## You can do this without asking

- **Refer to the project** factually, for example "based on Flare", "a fork of Flare", "works with
  Flare servers" or "compatible with the Flare API", including in articles, talks, tutorials and
  comparisons.
- **Run a Flare server**, privately or publicly, and say that it runs Flare (for example "ACME's Flare
  server at flare.acme.com").
- **Build and use the apps yourself or inside your organisation** with the official branding, modified
  or not, as long as you don't distribute them to the public.
- **Package unmodified releases** for an operating system or software repository (a Linux
  distribution, Homebrew, Flathub, F-Droid and similar) under the Flare name. Your changes must be
  limited to packaging, build fixes or security fixes, and you must not present yourself as the
  project.
- **Link to the official builds** using the official name and logo.

## You need your own name and logo for this

- **Publishing your own build of the apps**, whether modified or just rebuilt from source, in an app
  store, on a website, or anywhere else the public can download it (apart from the packaging case
  above). Use your own name, logo and app identifiers. You may describe it as "based on Flare".
- **Naming a product, service, company or domain** in a way that suggests it is made, run or endorsed
  by the Flare project, such as "Flare Cloud" or "Flare Pro". Saying "powered by Flare" in a description
  is fine.
- **Using a modified version of the logo**, or a logo confusingly similar to it.

## The official push relay

The official push relay delivers notifications only to the official apps. Rebranded apps run their own
relay, or their servers push to them directly (see
[docs/ARCHITECTURE.md §5.3](docs/ARCHITECTURE.md#53-push-credentials-and-the-push-relay)).

## Questions and permission

If you're unsure whether your use is allowed, or want permission for something this policy doesn't
cover, [open an issue](https://github.com/vignesh-ramsh/flare/issues) before you publish.

This policy may change over time; the version on the default branch applies.
