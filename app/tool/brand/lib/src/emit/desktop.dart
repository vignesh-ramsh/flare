import '../icons.dart';
import '../image_io.dart';
import '../loader.dart';
import '../outputs.dart';

/// Escapes [value] for XML text and attribute values.
String xmlEscape(String value) => value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&apos;');

/// Sizes stored in the Windows `.ico`.
const _icoSizes = [16, 24, 32, 48, 64, 128, 256];

/// Sizes of the Linux `hicolor` theme icons.
const _hicolorSizes = [16, 32, 48, 64, 128, 256, 512];

/// Windows: `brand.cmake` for the runner and its `app_icon.ico`.
///
/// Returns the `.ico` bytes so the Windows tray icon can reuse them.
List<int> emitWindows(Brand brand, IconFactory icons, Outputs out) {
  final s = brand.spec;
  out.text('windows/brand.cmake', '''
# $generatedNotice
set(BRAND_APP_NAME "${s.name}")
set(BRAND_BINARY_NAME "${s.binaryName}")
set(BRAND_DESCRIPTION "${s.description}")
set(BRAND_WINDOWS_IDENTITY "${s.windowsId}")
set(BRAND_WINDOWS_PUBLISHER "${s.windowsPublisher}")
set(BRAND_URL_SCHEME "${s.urlScheme}")
''');
  final ico = encodeIco([
    for (final size in _icoSizes) icons.rasterRounded(size),
  ]);
  out.bytes('windows/runner/resources/app_icon.ico', ico);
  return ico;
}

/// Linux: `brand.cmake`, the `.desktop` entry (with a handler for the URL
/// scheme), AppStream metadata and `hicolor` icons.
void emitLinux(Brand brand, IconFactory icons, Outputs out) {
  final s = brand.spec;
  const dir = 'linux/brand';

  out.text('linux/brand.cmake', '''
# $generatedNotice
set(BRAND_APP_NAME "${s.name}")
set(BRAND_BINARY_NAME "${s.binaryName}")
set(BRAND_APPLICATION_ID "${s.linuxId}")
set(BRAND_DESCRIPTION "${s.description}")
set(BRAND_URL_SCHEME "${s.urlScheme}")
''');

  out.text('$dir/${s.linuxId}.desktop', '''
# $generatedNotice
[Desktop Entry]
Type=Application
Name=${s.name}
Comment=${s.description}
Exec=${s.binaryName} %u
Icon=${s.linuxId}
Terminal=false
Categories=Network;Utility;
Keywords=notifications;push;alerts;
StartupWMClass=${s.linuxId}
MimeType=x-scheme-handler/${s.urlScheme};
''');

  out.text('$dir/${s.linuxId}.metainfo.xml', '''
<?xml version="1.0" encoding="UTF-8"?>
<!-- $generatedNotice -->
<component type="desktop-application">
  <id>${xmlEscape(s.linuxId)}</id>
  <name>${xmlEscape(s.name)}</name>
  <summary>${xmlEscape(s.description)}</summary>
  <metadata_license>CC0-1.0</metadata_license>
  <project_license>Apache-2.0</project_license>
  <description>
    <p>${xmlEscape(s.description)}</p>
  </description>
  <launchable type="desktop-id">${xmlEscape(s.linuxId)}.desktop</launchable>
${[if (s.website.isNotEmpty) '  <url type="homepage">${xmlEscape(s.website)}</url>', if (s.support.isNotEmpty) '  <url type="bugtracker">${xmlEscape(s.support)}</url>', if (s.sourceCode.isNotEmpty) '  <url type="vcs-browser">${xmlEscape(s.sourceCode)}</url>'].join('\n')}
  <provides>
    <binary>${xmlEscape(s.binaryName)}</binary>
  </provides>
  <branding>
    <color type="primary" scheme_preference="light">${s.primaryColor.hex}</color>
    <color type="primary" scheme_preference="dark">${s.primaryColor.hex}</color>
  </branding>
  <content_rating type="oars-1.1" />
</component>
''');

  for (final size in _hicolorSizes) {
    out.bytes(
      '$dir/icons/hicolor/${size}x$size/apps/${s.linuxId}.png',
      icons.rounded(size),
    );
  }
}

/// Images the app loads itself: the in-app logo and the system-tray icons.
/// [windowsIco] is reused as the Windows tray icon.
void emitAssets(
  Brand brand,
  IconFactory icons,
  Outputs out,
  List<int> windowsIco,
) {
  out
    ..bytes('assets/brand/logo.png', icons.rounded(512))
    ..bytes('assets/brand/tray_linux.png', icons.trayLight(64))
    ..bytes('assets/brand/tray_macos_template.png', icons.trayTemplate(44))
    ..bytes('assets/brand/tray_windows.ico', windowsIco);
}

/// Store listing fields that come from the brand, in the layout Fastlane reads.
/// Everything else in the listing (full descriptions, screenshots, release
/// notes) is written by hand.
void emitFastlane(Brand brand, Outputs out) {
  final s = brand.spec;
  out
    ..text('fastlane/metadata/android/en-US/title.txt', s.name)
    ..text(
      'fastlane/metadata/android/en-US/short_description.txt',
      s.description,
    )
    ..text('fastlane/metadata/en-US/name.txt', s.name);
  if (s.privacyPolicy.isNotEmpty) {
    out.text('fastlane/metadata/en-US/privacy_url.txt', s.privacyPolicy);
  }
  if (s.support.isNotEmpty) {
    out.text('fastlane/metadata/en-US/support_url.txt', s.support);
  }
  if (s.website.isNotEmpty) {
    out.text('fastlane/metadata/en-US/marketing_url.txt', s.website);
  }
}
