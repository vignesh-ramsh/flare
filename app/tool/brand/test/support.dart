import 'dart:io';

import 'package:brand_tool/brand_tool.dart';
import 'package:brand_tool/src/image_io.dart';
import 'package:brand_tool/src/raster.dart';
import 'package:path/path.dart' as p;
import 'package:test/test.dart';

/// The repository root: the nearest parent that has `brand/brand.yaml`.
Directory get repoRoot {
  var dir = Directory.current.absolute;
  while (true) {
    if (File(p.join(dir.path, 'brand', 'brand.yaml')).existsSync()) return dir;
    final parent = dir.parent;
    if (parent.path == dir.path) {
      throw StateError(
        'Run the tests from inside the repository (cwd: ${Directory.current.path})',
      );
    }
    dir = parent;
  }
}

/// The official brand.
Directory get flareBrand => Directory(p.join(repoRoot.path, 'brand'));

/// The example brand used to prove that rebranding works.
Directory get acmeBrand =>
    Directory(p.join(repoRoot.path, 'brand', 'examples', 'acme'));

/// A new temporary folder, deleted when the current test ends.
Directory tempDir() {
  final dir = Directory.systemTemp.createTempSync('brand_test_');
  addTearDown(() => dir.deleteSync(recursive: true));
  return dir;
}

/// Edits `brand.yaml` text: replaces [from] with [to], failing loudly if [from]
/// is not there so a test can never pass by editing nothing.
String Function(String) swap(String from, String to) => (yaml) {
  if (!yaml.contains(from)) throw StateError('"$from" is not in brand.yaml');
  return yaml.replaceFirst(from, to);
};

/// Copies a brand folder's `brand.yaml` and images to a temporary folder,
/// applying [edits] to the YAML.
Directory copyBrand(
  Directory source, {
  List<String Function(String)> edits = const [],
}) {
  final dir = tempDir();
  for (final name in ['brand.yaml', 'icon.png', 'symbol.png']) {
    File(p.join(source.path, name)).copySync(p.join(dir.path, name));
  }
  final yaml = File(p.join(dir.path, 'brand.yaml'));
  yaml.writeAsStringSync(
    edits.fold(yaml.readAsStringSync(), (text, edit) => edit(text)),
  );
  return dir;
}

/// Loads [dir], returning the brand (null if invalid) and everything reported.
(Brand?, Report) load(
  Directory dir, {
  bool release = false,
  Directory? upstream,
}) {
  final report = Report();
  final brand = loadBrand(dir, report, release: release, upstream: upstream);
  return (brand, report);
}

/// Loads a brand that must be valid.
Brand loadValid(Directory dir) {
  final (brand, report) = load(dir);
  expect(report.errors, isEmpty);
  return brand!;
}

void writePng(Directory dir, String name, Raster image) =>
    File(p.join(dir.path, name)).writeAsBytesSync(encodePng(image));

/// A black filled circle on a transparent background.
Raster disk(int size, {double radius = 0.4, Rgb color = Rgb.black}) {
  final image = Raster(size, size);
  for (var y = 0; y < size; y++) {
    for (var x = 0; x < size; x++) {
      final dx = (x + 0.5) / size - 0.5, dy = (y + 0.5) / size - 0.5;
      if (dx * dx + dy * dy <= radius * radius) {
        final i = (y * size + x) * 4;
        image.pixels[i] = color.r;
        image.pixels[i + 1] = color.g;
        image.pixels[i + 2] = color.b;
        image.pixels[i + 3] = 255;
      }
    }
  }
  return image;
}

/// A [fraction]-wide opaque square in the middle of a transparent canvas.
Raster centeredSquare(int size, double fraction, Rgb color) {
  final inner = (size * fraction).round();
  return Raster(size, size)..drawOver(
    Raster.solid(inner, inner, color),
    dx: (size - inner) ~/ 2,
    dy: (size - inner) ~/ 2,
  );
}
