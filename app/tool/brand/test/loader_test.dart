import 'dart:convert';
import 'dart:io';

import 'package:brand_tool/brand_tool.dart';
import 'package:brand_tool/src/raster.dart';
import 'package:path/path.dart' as p;
import 'package:test/test.dart';

import 'support.dart';

void main() {
  test('the official brand and the example brand load cleanly', () {
    for (final dir in [flareBrand, acmeBrand]) {
      final (brand, report) = load(dir);
      expect(report.errors, isEmpty, reason: dir.path);
      expect(report.warnings, isEmpty, reason: dir.path);
      expect(brand!.icon.width, 1024);
      expect(brand.symbol.isSquare, isTrue);
    }
  });

  test('a missing folder or brand.yaml is an error, not an exception', () {
    expect(load(Directory(p.join(tempDir().path, 'nope'))).$2.errors, [
      contains('brand.yaml: not found'),
    ]);
    expect(load(tempDir()).$2.errors, [contains('brand.yaml: not found')]);
  });

  group('icon.png', () {
    Report iconReport(Raster icon) {
      final dir = copyBrand(flareBrand);
      writePng(dir, 'icon.png', icon);
      return load(dir).$2;
    }

    test('must be a PNG', () {
      final dir = copyBrand(flareBrand);
      File(
        p.join(dir.path, 'icon.png'),
      ).writeAsBytesSync(utf8.encode('GIF89a not a png'));
      expect(load(dir).$2.errors, [
        contains('look.icon: icon.png is not a PNG'),
      ]);
    });

    test('must not be damaged', () {
      final dir = copyBrand(flareBrand);
      final bytes = File(p.join(dir.path, 'icon.png')).readAsBytesSync();
      File(
        p.join(dir.path, 'icon.png'),
      ).writeAsBytesSync(bytes.sublist(0, 200));
      expect(load(dir).$2.errors, [contains('damaged')]);
    });

    test('must be square', () {
      expect(iconReport(Raster.solid(1024, 512, Rgb.white)).errors, [
        contains('1024x512; it must be square'),
      ]);
    });

    test('must be at least 1024 pixels', () {
      expect(iconReport(Raster.solid(512, 512, Rgb.white)).errors, [
        contains('at least 1024x1024'),
      ]);
    });

    test('larger artwork is scaled down to 1024', () {
      final dir = copyBrand(flareBrand);
      writePng(dir, 'icon.png', centeredSquare(2048, 0.5, Rgb.black));
      final (brand, report) = load(dir);
      expect(report.errors, isEmpty);
      expect(brand!.icon.width, 1024);
    });

    test('artwork outside the safe zone gets a warning, not an error', () {
      final report = iconReport(centeredSquare(1024, 0.9, Rgb.black));
      expect(report.errors, isEmpty);
      expect(report.warnings, [contains('central two thirds')]);
    });

    test('a full-bleed opaque icon is not warned about', () {
      expect(iconReport(Raster.solid(1024, 1024, Rgb.black)).warnings, isEmpty);
    });
  });

  group('symbol.png', () {
    Report symbolReport(Raster symbol) {
      final dir = copyBrand(flareBrand);
      writePng(dir, 'symbol.png', symbol);
      return load(dir).$2;
    }

    test('a solid image would show as a square, so it is refused', () {
      expect(symbolReport(Raster.solid(256, 256, Rgb.black)).errors, [
        contains('no transparent background'),
      ]);
    });

    test('an empty image is refused', () {
      expect(symbolReport(Raster(256, 256)).errors, [contains('is empty')]);
    });

    test('must be square and at least 256 pixels', () {
      expect(symbolReport(disk(255)).errors, [contains('at least 256x256')]);
      expect(
        symbolReport(
          Raster(300, 256)..pixels.fillRange(0, 4 * 300 * 100, 255),
        ).errors,
        [contains('must be square')],
      );
    });

    test('several colours get a warning, since only the shape is used', () {
      final image = disk(256)..pixels.fillRange(0, 4 * 256 * 128, 0);
      for (var i = 0; i < image.pixels.length; i += 4 * 3) {
        if (image.pixels[i + 3] > 0) {
          image.pixels.setRange(i, i + 3, [255, 255, 255]);
        }
      }
      final report = symbolReport(image);
      expect(report.errors, isEmpty);
      expect(report.warnings, [contains('several colours')]);
    });

    test('is trimmed to a square around the shape', () {
      final dir = copyBrand(flareBrand);
      writePng(
        dir,
        'symbol.png',
        Raster(512, 512)..drawOver(disk(128), dx: 100, dy: 300),
      );
      final (brand, report) = load(dir);
      expect(report.errors, isEmpty);
      expect(brand!.symbol.width, inInclusiveRange(98, 102));
    });
  });

  group('file paths', () {
    test('a missing file is named', () {
      final dir = copyBrand(
        flareBrand,
        edits: [swap('icon: icon.png', 'icon: nothing.png')],
      );
      expect(load(dir).$2.errors, [contains('file not found: nothing.png')]);
    });

    test('absolute paths are refused', () {
      final dir = copyBrand(
        flareBrand,
        edits: [swap('icon: icon.png', 'icon: /etc/hostname')],
      );
      expect(load(dir).$2.errors, [contains('not an absolute path')]);
    });

    test('.. cannot escape the brand folder', () {
      final outer = tempDir();
      final dir = Directory(p.join(outer.path, 'brand'))..createSync();
      for (final name in ['brand.yaml', 'symbol.png']) {
        File(p.join(flareBrand.path, name)).copySync(p.join(dir.path, name));
      }
      File(
        p.join(flareBrand.path, 'icon.png'),
      ).copySync(p.join(outer.path, 'outside.png'));
      File(p.join(dir.path, 'brand.yaml')).writeAsStringSync(
        swap('icon: icon.png', 'icon: ../outside.png')(
          File(p.join(dir.path, 'brand.yaml')).readAsStringSync(),
        ),
      );
      expect(load(dir).$2.errors, [
        contains('must stay inside the brand folder'),
      ]);
    });

    test('a symlink cannot smuggle in a file from outside', () {
      final outer = tempDir();
      final dir = copyBrand(flareBrand);
      final target = File(
        p.join(outer.path, 'secret.png'),
      )..writeAsBytesSync(File(p.join(dir.path, 'icon.png')).readAsBytesSync());
      File(p.join(dir.path, 'icon.png')).deleteSync();
      Link(p.join(dir.path, 'icon.png')).createSync(target.path);
      expect(load(dir).$2.errors, [
        contains('must stay inside the brand folder'),
      ]);
    });
  });

  group('firebase config', () {
    String googleServices(String package) => jsonEncode({
      'project_info': {'project_id': 'demo'},
      'client': [
        {
          'client_info': {
            'android_client_info': {'package_name': package},
          },
        },
      ],
    });

    Directory withFirebase(String contents) {
      final dir = copyBrand(
        flareBrand,
        edits: [
          swap(
            'android_config: ""',
            'android_config: firebase/google-services.json',
          ),
        ],
      );
      Directory(p.join(dir.path, 'firebase')).createSync();
      File(
        p.join(dir.path, 'firebase', 'google-services.json'),
      ).writeAsStringSync(contents);
      return dir;
    }

    test('a file for the right app is accepted and kept', () {
      final (brand, report) = load(
        withFirebase(googleServices('com.example.flare')),
      );
      expect(report.errors, isEmpty);
      expect(utf8.decode(brand!.googleServices!), contains('demo'));
    });

    test('a file for another app is the classic mistake, and is refused', () {
      final (_, report) = load(withFirebase(googleServices('com.other.app')));
      expect(report.errors, [
        allOf(
          contains('no Android app for "com.example.flare"'),
          contains('com.other.app'),
        ),
      ]);
    });

    test('anything that is not a google-services.json is refused', () {
      for (final contents in ['not json', '{}', '{"client": [{}]}', '[]']) {
        expect(load(withFirebase(contents)).$2.errors, [
          contains('not a valid google-services.json'),
        ], reason: contents);
      }
    });
  });

  group('release mode', () {
    test('the placeholder identifiers of the official brand are refused', () {
      final (brand, report) = load(flareBrand, release: true);
      expect(brand, isNull);
      expect(
        report.errors,
        containsAll([
          contains('ids.android'),
          contains('ids.apple:'),
          contains('ids.windows:'),
          contains('ids.linux'),
          contains('ids.windows_publisher'),
          contains('links.privacy_policy'),
        ]),
      );
      expect(report.warnings, [contains('ids.apple_team_id')]);
    });

    test('placeholder web addresses are refused, wherever they appear', () {
      final (_, report) = load(acmeBrand, release: true);
      expect(
        report.errors,
        containsAll([
          contains('defaults.server_url'),
          contains('links.website'),
          contains('links.support'),
          contains('links.privacy_policy'),
        ]),
      );
    });

    final realBrand = [
      swap('android: com.example.flare ', 'android: org.acme.beep '),
      swap('apple: com.example.flare ', 'apple: org.acme.beep '),
      swap('apple_team_id: ""', 'apple_team_id: "ABCDE12345"'),
      swap('windows: Example.Flare', 'windows: Acme.Beep'),
      swap(
        'windows_publisher: "CN=Example"',
        'windows_publisher: "CN=Acme Corp"',
      ),
      swap('linux: com.example.Flare', 'linux: org.acme.Beep'),
      swap('privacy_policy: ""', 'privacy_policy: https://acme.org/privacy'),
      swap('name: Flare ', 'name: Beep '),
    ];

    test('a brand with real values passes', () {
      final (brand, report) = load(
        copyBrand(flareBrand, edits: realBrand),
        release: true,
        upstream: flareBrand,
      );
      expect(report.errors, isEmpty);
      expect(report.warnings, isEmpty);
      expect(brand, isNotNull);
    });

    test(
      'keeping the name "Flare" in a rebrand is flagged as a trademark problem',
      () {
        final edits = [...realBrand]..removeLast();
        final (_, report) = load(
          copyBrand(flareBrand, edits: edits),
          release: true,
          upstream: flareBrand,
        );
        expect(report.warnings, [
          allOf(contains('app.name'), contains('TRADEMARKS.md')),
        ]);
      },
    );

    test(
      "reusing the project's own identifiers under another name is flagged",
      () {
        final edits = [
          swap('name: Flare ', 'name: Beep '),
          swap(
            'privacy_policy: ""',
            'privacy_policy: https://acme.org/privacy',
          ),
        ];
        final (_, report) = load(
          copyBrand(flareBrand, edits: edits),
          release: true,
          upstream: flareBrand,
        );
        expect(
          report.warnings,
          contains(
            allOf(
              contains('ids.android'),
              contains("same as Flare's own identifiers"),
            ),
          ),
        );
      },
    );

    test('the official brand is never compared with itself', () {
      final (_, report) = load(flareBrand, release: true, upstream: flareBrand);
      expect(
        report.warnings.where(
          (w) => w.contains('trademark') || w.contains('same as'),
        ),
        isEmpty,
      );
    });

    test('release rules do not apply to ordinary builds', () {
      expect(load(flareBrand).$2.errors, isEmpty);
    });
  });

  test('the loader never modifies the brand folder', () {
    final dir = copyBrand(acmeBrand);
    String snapshot() => [
      for (final f
          in dir.listSync().whereType<File>().toList()
            ..sort((a, b) => a.path.compareTo(b.path)))
        '${p.basename(f.path)}:${f.lengthSync()}:${f.readAsBytesSync().fold<int>(0, (h, b) => (h * 31 + b) & 0x7FFFFFFF)}',
    ].join('|');
    final before = snapshot();
    load(dir);
    expect(snapshot(), before);
  });
}
