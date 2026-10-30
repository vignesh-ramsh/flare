import 'dart:io';
import 'dart:typed_data';

import 'package:path/path.dart' as p;

import 'emit/android.dart';
import 'emit/apple.dart';
import 'emit/dart.dart';
import 'emit/desktop.dart';
import 'icons.dart';
import 'loader.dart';
import 'outputs.dart';

/// Folders, relative to the app folder, that belong entirely to the generator.
/// They are emptied before every run, so nothing from a previous brand lingers.
const ownedDirectories = [
  'android/app/src/brand',
  'ios/Runner/Assets.xcassets/AppIcon.appiconset',
  'macos/Runner/Assets.xcassets/AppIcon.appiconset',
  'linux/brand',
  'assets/brand',
];

/// Single files that belong to the generator. A file in this list that a brand
/// does not produce (such as `google-services.json`) is deleted.
const ownedFiles = [
  'lib/brand/brand.g.dart',
  'android/brand.properties',
  'android/app/google-services.json',
  'ios/Flutter/Brand.xcconfig',
  'macos/Runner/Configs/Brand.xcconfig',
  'windows/brand.cmake',
  'windows/runner/resources/app_icon.ico',
  'linux/brand.cmake',
  'fastlane/metadata/android/en-US/title.txt',
  'fastlane/metadata/android/en-US/short_description.txt',
  'fastlane/metadata/en-US/name.txt',
  'fastlane/metadata/en-US/privacy_url.txt',
  'fastlane/metadata/en-US/support_url.txt',
  'fastlane/metadata/en-US/marketing_url.txt',
];

bool _isOwned(String path) =>
    ownedFiles.contains(path) ||
    ownedDirectories.any((d) => p.isWithin(d, path));

/// Renders every generated file for [brand], in memory.
Outputs renderBrand(Brand brand) {
  final out = Outputs();
  final icons = IconFactory(brand);
  emitDart(brand, out);
  emitAndroid(brand, icons, out);
  emitApple(brand, icons, out);
  final ico = emitWindows(brand, icons, out);
  emitLinux(brand, icons, out);
  emitAssets(brand, icons, out, ico);
  emitFastlane(brand, out);
  return out;
}

/// Writes [outputs] into [appDir], first removing what an earlier run left in
/// the generator's own folders and files.
void writeOutputs(Directory appDir, Outputs outputs) {
  for (final path in outputs.files.keys) {
    if (!_isOwned(path)) {
      throw StateError(
        'Generator would write $path, which is not in its owned paths',
      );
    }
  }

  for (final dir in ownedDirectories) {
    final path = p.join(appDir.path, dir);
    if (FileSystemEntity.isLinkSync(path)) {
      Link(path).deleteSync();
    } else if (Directory(path).existsSync()) {
      Directory(path).deleteSync(recursive: true);
    }
  }
  for (final file in ownedFiles) {
    if (!outputs.files.containsKey(file)) {
      final f = File(p.join(appDir.path, file));
      if (f.existsSync()) f.deleteSync();
    }
  }

  for (final MapEntry(key: path, value: bytes) in outputs.files.entries) {
    final file = File(p.join(appDir.path, path));
    file.parent.createSync(recursive: true);
    file.writeAsBytesSync(Uint8List.fromList(bytes));
  }
}
