import 'dart:convert';

import '../icons.dart';
import '../loader.dart';
import '../outputs.dart';

const _json = JsonEncoder.withIndent('  ');

String _xcconfig(List<(String, String)> settings) => [
  '// $generatedNotice',
  for (final (key, value) in settings)
    value.isEmpty ? '$key =' : '$key = $value',
].join('\n');

String _points(num pt) => pt == pt.truncate() ? '${pt.toInt()}' : '$pt';

/// iOS app icons: (idiom, size in points, scale).
const _iosIcons = <(String, num, int)>[
  ('iphone', 20, 2),
  ('iphone', 20, 3),
  ('iphone', 29, 2),
  ('iphone', 29, 3),
  ('iphone', 40, 2),
  ('iphone', 40, 3),
  ('iphone', 60, 2),
  ('iphone', 60, 3),
  ('ipad', 20, 1),
  ('ipad', 20, 2),
  ('ipad', 29, 1),
  ('ipad', 29, 2),
  ('ipad', 40, 1),
  ('ipad', 40, 2),
  ('ipad', 76, 1),
  ('ipad', 76, 2),
  ('ipad', 83.5, 2),
  ('ios-marketing', 1024, 1),
];

/// macOS app icons: (size in points, scale).
const _macosIcons = <(int, int)>[
  (16, 1),
  (16, 2),
  (32, 1),
  (32, 2),
  (128, 1),
  (128, 2),
  (256, 1),
  (256, 2),
  (512, 1),
  (512, 2),
];

/// iOS and macOS: build settings (`Brand.xcconfig`) and `AppIcon.appiconset`.
///
/// The Xcode projects read the settings through `$(BRAND_...)` variables, for the
/// bundle ID, display name, URL scheme, and for the iOS App Group and the
/// notification extension's bundle ID.
void emitApple(Brand brand, IconFactory icons, Outputs out) {
  final s = brand.spec;

  out.text(
    'ios/Flutter/Brand.xcconfig',
    _xcconfig([
      ('BRAND_APP_NAME', s.name),
      ('BRAND_BUNDLE_ID', s.appleId),
      ('BRAND_EXTENSION_BUNDLE_ID', s.appleExtensionId),
      ('BRAND_APP_GROUP', s.appleAppGroup),
      ('BRAND_TEAM_ID', s.appleTeamId),
      ('BRAND_URL_SCHEME', s.urlScheme),
      ('BRAND_APP_LINKS_DOMAIN', s.appLinksDomain),
    ]),
  );

  out.text(
    'macos/Runner/Configs/Brand.xcconfig',
    _xcconfig([
      ('BRAND_APP_NAME', s.name),
      ('BRAND_BUNDLE_ID', s.appleId),
      ('BRAND_TEAM_ID', s.appleTeamId),
      ('BRAND_URL_SCHEME', s.urlScheme),
      ('BRAND_APP_LINKS_DOMAIN', s.appLinksDomain),
    ]),
  );

  const ios = 'ios/Runner/Assets.xcassets/AppIcon.appiconset';
  final iosImages = <Map<String, String>>[];
  final iosFiles = <String>{};
  for (final (idiom, pt, scale) in _iosIcons) {
    final size = '${_points(pt)}x${_points(pt)}';
    final file = 'Icon-App-$size@${scale}x.png';
    iosImages.add({
      'idiom': idiom,
      'size': size,
      'scale': '${scale}x',
      'filename': file,
    });
    // iPhone and iPad share some sizes; both entries point at the one file.
    if (iosFiles.add(file)) {
      out.bytes('$ios/$file', icons.ios((pt * scale).round()));
    }
  }
  out.text(
    '$ios/Contents.json',
    _json.convert({
      'images': iosImages,
      'info': {'version': 1, 'author': 'xcode'},
    }),
  );

  const macos = 'macos/Runner/Assets.xcassets/AppIcon.appiconset';
  final macosImages = <Map<String, String>>[];
  final macosFiles = <String>{};
  for (final (pt, scale) in _macosIcons) {
    final pixels = pt * scale;
    final file = 'app_icon_$pixels.png';
    macosImages.add({
      'idiom': 'mac',
      'size': '${pt}x$pt',
      'scale': '${scale}x',
      'filename': file,
    });
    if (macosFiles.add(file)) out.bytes('$macos/$file', icons.macos(pixels));
  }
  out.text(
    '$macos/Contents.json',
    _json.convert({
      'images': macosImages,
      'info': {'version': 1, 'author': 'xcode'},
    }),
  );
}
