import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:path/path.dart' as p;
import 'package:yaml/yaml.dart';

import 'image_io.dart';
import 'raster.dart';
import 'spec.dart';

/// Size every icon is produced from.
const masterSize = 1024;

/// A brand folder, validated and loaded.
final class Brand {
  Brand({
    required this.spec,
    required this.icon,
    required this.symbol,
    this.googleServices,
  });

  final BrandSpec spec;

  /// App icon artwork, [masterSize] x [masterSize].
  final Raster icon;

  /// Notification/tray silhouette, trimmed to its shape and square.
  final Raster symbol;

  /// Contents of `google-services.json`, if the brand has one.
  final Uint8List? googleServices;
}

/// Loads and validates the brand folder [dir]. Returns null if there are
/// errors; all problems, including warnings, are in [report].
///
/// With [release], placeholder identifiers and URLs, and a missing privacy
/// policy, are errors instead of being let through. [upstream] is the project's
/// own brand folder, used to spot builds that reuse its identity.
Brand? loadBrand(
  Directory dir,
  Report report, {
  bool release = false,
  Directory? upstream,
}) {
  final yamlFile = File(p.join(dir.path, 'brand.yaml'));
  if (!dir.existsSync() || !yamlFile.existsSync()) {
    report.error('brand.yaml', 'not found in ${dir.path}');
    return null;
  }
  final spec = parseBrandYaml(yamlFile.readAsStringSync(), report);
  if (spec == null) return null;

  final icon = _loadIcon(dir, spec, report);
  final symbol = _loadSymbol(dir, spec, report);
  final googleServices = _loadGoogleServices(dir, spec, report);

  if (release) _checkRelease(dir, spec, report, upstream);

  if (report.hasErrors || icon == null || symbol == null) return null;
  return Brand(
    spec: spec,
    icon: icon,
    symbol: symbol,
    googleServices: googleServices,
  );
}

/// Resolves [relative] inside [root], refusing absolute paths and anything that
/// escapes the folder (including through symlinks), so a brand folder can't make
/// the tool read arbitrary files.
File? _resolve(Directory root, String relative, String where, Report report) {
  if (p.isAbsolute(relative)) {
    report.error(
      where,
      'must be a path inside the brand folder, not an absolute path',
    );
    return null;
  }
  final file = File(p.join(root.path, relative));
  if (!file.existsSync()) {
    report.error(
      where,
      'file not found: $relative (paths are relative to the brand folder)',
    );
    return null;
  }
  final realRoot = root.resolveSymbolicLinksSync();
  final realFile = file.resolveSymbolicLinksSync();
  if (!p.isWithin(realRoot, realFile)) {
    report.error(where, 'must stay inside the brand folder (found $relative)');
    return null;
  }
  return File(realFile);
}

Raster? _loadIcon(Directory dir, BrandSpec spec, Report report) {
  const where = 'look.icon';
  final file = _resolve(dir, spec.iconFile, where, report);
  if (file == null) return null;
  final bytes = file.readAsBytesSync();
  if (!looksLikePng(bytes)) {
    report.error(where, '${spec.iconFile} is not a PNG file');
    return null;
  }
  final image = decodePng(bytes);
  if (image == null) {
    report.error(where, '${spec.iconFile} is damaged and could not be decoded');
    return null;
  }
  if (!image.isSquare) {
    report.error(
      where,
      '${spec.iconFile} is ${image.width}x${image.height}; it must be square',
    );
    return null;
  }
  if (image.width < masterSize) {
    report.error(
      where,
      '${spec.iconFile} is ${image.width}px; it must be at least ${masterSize}x$masterSize',
    );
    return null;
  }
  final icon = image.resized(masterSize, masterSize);

  // Android crops adaptive icons to a shape such as a circle; only the middle
  // two thirds of the canvas is guaranteed to stay visible.
  if (icon.alphaShare((a) => a < 16) > 0.05) {
    var visible = 0, outside = 0;
    const safeRadius = 0.34 * masterSize;
    for (var y = 0; y < masterSize; y++) {
      for (var x = 0; x < masterSize; x++) {
        if (icon.pixels[(y * masterSize + x) * 4 + 3] < 128) continue;
        visible++;
        final dx = x + 0.5 - masterSize / 2, dy = y + 0.5 - masterSize / 2;
        if (math.sqrt(dx * dx + dy * dy) > safeRadius) outside++;
      }
    }
    if (visible > 0 && outside / visible > 0.005) {
      report.warn(
        where,
        'the artwork extends beyond the central two thirds of ${spec.iconFile}; Android may crop it on round or squircle launchers',
      );
    }
  }
  return icon;
}

Raster? _loadSymbol(Directory dir, BrandSpec spec, Report report) {
  const where = 'look.symbol';
  final file = _resolve(dir, spec.symbolFile, where, report);
  if (file == null) return null;
  final bytes = file.readAsBytesSync();
  if (!looksLikePng(bytes)) {
    report.error(where, '${spec.symbolFile} is not a PNG file');
    return null;
  }
  final image = decodePng(bytes);
  if (image == null) {
    report.error(
      where,
      '${spec.symbolFile} is damaged and could not be decoded',
    );
    return null;
  }
  if (!image.isSquare) {
    report.error(
      where,
      '${spec.symbolFile} is ${image.width}x${image.height}; it must be square',
    );
    return null;
  }
  if (image.width < 256) {
    report.error(
      where,
      '${spec.symbolFile} is ${image.width}px; it must be at least 256x256',
    );
    return null;
  }
  if (image.alphaShare((a) => a < 16) < 0.01) {
    report.error(
      where,
      '${spec.symbolFile} has no transparent background. Notification and tray icons use only the shape (the alpha channel), so a solid image would show up as a plain square. Export the silhouette on a transparent background.',
    );
    return null;
  }
  if (image.alphaShare((a) => a >= 128) < 0.005) {
    report.error(
      where,
      '${spec.symbolFile} is empty: it has almost no visible pixels',
    );
    return null;
  }

  // Only the shape matters, but several colours usually mean a full-colour logo
  // was exported by mistake.
  var lowest = 255, highest = 0;
  for (var i = 0; i < image.pixels.length; i += 4) {
    if (image.pixels[i + 3] < 128) continue;
    final luma =
        (image.pixels[i] * 299 +
            image.pixels[i + 1] * 587 +
            image.pixels[i + 2] * 114) ~/
        1000;
    lowest = math.min(lowest, luma);
    highest = math.max(highest, luma);
  }
  if (highest - lowest > 64) {
    report.warn(
      where,
      '${spec.symbolFile} contains several colours; only its shape is used, so they will be flattened to one colour',
    );
  }
  return image.trimmedToSquare();
}

Uint8List? _loadGoogleServices(Directory dir, BrandSpec spec, Report report) {
  if (spec.firebaseAndroidConfig.isEmpty) return null;
  const where = 'firebase.android_config';
  final file = _resolve(dir, spec.firebaseAndroidConfig, where, report);
  if (file == null) return null;
  final bytes = file.readAsBytesSync();
  try {
    final json = jsonDecode(utf8.decode(bytes)) as Map<String, Object?>;
    final packages = <String>[
      for (final client in json['client'] as List<Object?>)
        ((((client as Map<String, Object?>)['client_info']
                    as Map<String, Object?>)['android_client_info']
                as Map<String, Object?>)['package_name'])
            as String,
    ];
    if (!packages.contains(spec.androidId)) {
      report.error(
        where,
        'google-services.json has no Android app for "${spec.androidId}" (it has: ${packages.join(', ')}). '
        'Download the file for the app you registered under ids.android.',
      );
      return null;
    }
  } on Object {
    report.error(
      where,
      '${spec.firebaseAndroidConfig} is not a valid google-services.json',
    );
    return null;
  }
  return bytes;
}

// --- Release checks ---------------------------------------------------------

bool _isPlaceholderId(String id) {
  final lower = id.toLowerCase();
  return lower.startsWith('com.example.') ||
      lower.startsWith('example.') ||
      lower == 'com.example';
}

bool _isPlaceholderUrl(String url) {
  if (url.isEmpty) return false;
  final host = (Uri.tryParse(url)?.host ?? '').toLowerCase();
  const reserved = ['example.com', 'example.org', 'example.net'];
  return host == 'example' ||
      host.endsWith('.example') ||
      reserved.any((r) => host == r || host.endsWith('.$r'));
}

Map<String, String>? _readIds(Directory dir) {
  final file = File(p.join(dir.path, 'brand.yaml'));
  if (!file.existsSync()) return null;
  try {
    final ids =
        (loadYaml(file.readAsStringSync()) as YamlMap)['ids'] as YamlMap;
    return {for (final e in ids.entries) '${e.key}': '${e.value}'};
  } on Object {
    return null;
  }
}

void _checkRelease(
  Directory dir,
  BrandSpec spec,
  Report report,
  Directory? upstream,
) {
  for (final (key, id) in [
    ('ids.android', spec.androidId),
    ('ids.apple', spec.appleId),
    ('ids.windows', spec.windowsId),
    ('ids.linux', spec.linuxId),
  ]) {
    if (_isPlaceholderId(id)) {
      report.error(
        key,
        '"$id" is a placeholder. Release builds need identifiers that belong to you.',
      );
    }
  }
  if (spec.windowsPublisher.toLowerCase().contains('example')) {
    report.error(
      'ids.windows_publisher',
      '"${spec.windowsPublisher}" is a placeholder. It must match your code-signing certificate.',
    );
  }
  for (final (key, url) in [
    ('defaults.server_url', spec.serverUrl),
    ('defaults.push_relay_url', spec.pushRelayUrl),
    ('links.website', spec.website),
    ('links.support', spec.support),
    ('links.privacy_policy', spec.privacyPolicy),
    ('links.source_code', spec.sourceCode),
  ]) {
    if (_isPlaceholderUrl(url)) {
      report.error(
        key,
        '"$url" is a placeholder address. Use your real one, or leave it empty.',
      );
    }
  }
  if (spec.privacyPolicy.isEmpty) {
    report.error(
      'links.privacy_policy',
      'is required for release builds: the app stores reject apps without a privacy policy',
    );
  }
  if (spec.appleTeamId.isEmpty) {
    report.warn(
      'ids.apple_team_id',
      'is empty, so iOS and macOS builds can not be signed',
    );
  }

  // Compare with the project's own brand to catch half-finished rebrands.
  if (upstream == null || _sameDirectory(dir, upstream)) return;
  final upstreamIds = _readIds(upstream);
  if (upstreamIds == null) return;
  final reused = [
    for (final (key, id) in [
      ('ids.android', spec.androidId),
      ('ids.apple', spec.appleId),
      ('ids.windows', spec.windowsId),
      ('ids.linux', spec.linuxId),
    ])
      if (upstreamIds[key.substring(4)] == id) key,
  ];
  if (reused.isNotEmpty) {
    report.warn(
      reused.join(', '),
      'same as $upstreamName\'s own identifiers. The stores would reject a second app with them, and push '
      'notifications are tied to them. Choose your own.',
    );
  }
  if (spec.name.toLowerCase() == upstreamName.toLowerCase()) {
    report.warn(
      'app.name',
      'the name "$upstreamName" is a trademark of the $upstreamName project. Publishing your own build under it is not allowed; see TRADEMARKS.md.',
    );
  }
}

bool _sameDirectory(Directory a, Directory b) {
  try {
    return a.resolveSymbolicLinksSync() == b.resolveSymbolicLinksSync();
  } on FileSystemException {
    return false;
  }
}
