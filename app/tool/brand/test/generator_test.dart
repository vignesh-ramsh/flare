import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:brand_tool/brand_tool.dart';
import 'package:brand_tool/src/image_io.dart';
import 'package:path/path.dart' as p;
import 'package:test/test.dart';

import 'support.dart';

String text(Outputs out, String path) => utf8.decode(out.files[path]!);

/// Width and height from a PNG's IHDR chunk, without decoding the image.
(int, int) pngSize(Uint8List png) {
  final data = ByteData.sublistView(png);
  return (data.getUint32(16), data.getUint32(20));
}

void main() {
  late Outputs flare;
  late Outputs acme;

  setUpAll(() {
    flare = renderBrand(loadValid(flareBrand));
    acme = renderBrand(loadValid(acmeBrand));
  });

  group('what is generated', () {
    test('the same brand always gives byte-identical files', () {
      final again = renderBrand(loadValid(acmeBrand));
      expect(again.files.keys.toList(), acme.files.keys.toList());
      for (final path in acme.files.keys) {
        expect(again.files[path], acme.files[path], reason: path);
      }
    });

    test('every file is within the generator\'s owned paths', () {
      final dir = tempDir();
      writeOutputs(dir, flare); // throws if anything is outside them
      expect(
        dir.listSync(recursive: true).whereType<File>().length,
        flare.files.length,
      );
    });

    test('Android: properties, colours and icons at every density', () {
      expect(
        text(acme, 'android/brand.properties'),
        allOf(
          contains('applicationId=com.acme.alerts'),
          contains('appName=Acme\n'),
          contains('urlScheme=acme-alerts'),
        ),
      );
      expect(
        text(acme, 'android/app/src/brand/res/values/brand_colors.xml'),
        allOf(
          contains('name="ic_launcher_background">#1D4ED8<'),
          contains('name="notification_accent">#2563EB<'),
        ),
      );
      const res = 'android/app/src/brand/res';
      for (final (density, scale) in [
        ('mdpi', 1.0),
        ('hdpi', 1.5),
        ('xhdpi', 2.0),
        ('xxhdpi', 3.0),
        ('xxxhdpi', 4.0),
      ]) {
        int px(num dp) => (dp * scale).round();
        expect(pngSize(acme.files['$res/mipmap-$density/ic_launcher.png']!), (
          px(48),
          px(48),
        ));
        expect(
          pngSize(
            acme.files['$res/mipmap-$density/ic_launcher_foreground.png']!,
          ),
          (px(108), px(108)),
        );
        expect(
          pngSize(
            acme.files['$res/mipmap-$density/ic_launcher_monochrome.png']!,
          ),
          (px(108), px(108)),
        );
        expect(
          pngSize(acme.files['$res/drawable-$density/ic_notification.png']!),
          (px(24), px(24)),
        );
      }
      expect(
        text(acme, '$res/mipmap-anydpi-v26/ic_launcher.xml'),
        allOf(
          contains('@color/ic_launcher_background'),
          contains('@mipmap/ic_launcher_foreground'),
          contains('@mipmap/ic_launcher_monochrome'),
        ),
      );
    });

    test(
      'Android: the notification icon is a white silhouette with real transparency',
      () {
        final icon = decodePng(
          acme.files['android/app/src/brand/res/drawable-xxxhdpi/ic_notification.png']!,
        )!;
        var visible = 0, transparent = 0;
        for (var i = 0; i < icon.pixels.length; i += 4) {
          if (icon.pixels[i + 3] > 0) {
            visible++;
            expect(icon.pixels.sublist(i, i + 3), [255, 255, 255]);
          } else {
            transparent++;
          }
        }
        expect(visible, greaterThan(500));
        expect(transparent, greaterThan(visible ~/ 4));
      },
    );

    test(
      'iOS: a full icon set, with no alpha channel (the App Store rejects one)',
      () {
        const dir = 'ios/Runner/Assets.xcassets/AppIcon.appiconset';
        final contents =
            jsonDecode(text(acme, '$dir/Contents.json'))
                as Map<String, Object?>;
        final images = (contents['images'] as List<Object?>)
            .cast<Map<String, Object?>>();
        expect(images, hasLength(18));
        for (final image in images) {
          final file = image['filename'] as String;
          final png = acme.files['$dir/$file']!;
          expect(png[25], 2, reason: '$file must be RGB, colour type 2');
          final size = double.parse((image['size'] as String).split('x').first);
          final scale = int.parse(
            (image['scale'] as String).replaceAll('x', ''),
          );
          final pixels = (size * scale).round();
          expect(pngSize(png), (pixels, pixels), reason: file);
        }
        expect(images.map((i) => i['idiom']).toSet(), {
          'iphone',
          'ipad',
          'ios-marketing',
        });
      },
    );

    test(
      'iOS: the build settings carry the identities the Xcode project reads',
      () {
        expect(
          text(acme, 'ios/Flutter/Brand.xcconfig'),
          allOf(
            contains('BRAND_APP_NAME = Acme Alerts\n'),
            contains('BRAND_BUNDLE_ID = com.acme.alerts\n'),
            contains(
              'BRAND_EXTENSION_BUNDLE_ID = com.acme.alerts.NotificationService\n',
            ),
            contains('BRAND_APP_GROUP = group.com.acme.alerts\n'),
            contains('BRAND_TEAM_ID =\n'),
            contains('BRAND_URL_SCHEME = acme-alerts\n'),
          ),
        );
      },
    );

    test('macOS: icons for every size, with transparent margins', () {
      const dir = 'macos/Runner/Assets.xcassets/AppIcon.appiconset';
      final contents =
          jsonDecode(text(acme, '$dir/Contents.json')) as Map<String, Object?>;
      expect(contents['images'], hasLength(10));
      for (final size in [16, 32, 64, 128, 256, 512, 1024]) {
        expect(pngSize(acme.files['$dir/app_icon_$size.png']!), (size, size));
      }
      final big = decodePng(acme.files['$dir/app_icon_1024.png']!)!;
      expect(big.pixels[3], 0, reason: 'corner is transparent');
      final centre = (512 * 1024 + 512) * 4;
      expect(big.pixels[centre + 3], 255);
    });

    test('Windows: an .ico with every size, and the same one for the tray', () {
      final ico = acme.files['windows/runner/resources/app_icon.ico']!;
      expect(ByteData.sublistView(ico).getUint16(4, Endian.little), 7);
      expect(acme.files['assets/brand/tray_windows.ico'], ico);
      expect(
        text(acme, 'windows/brand.cmake'),
        allOf(
          contains('set(BRAND_APP_NAME "Acme Alerts")'),
          contains('set(BRAND_BINARY_NAME "alerts")'),
          contains('set(BRAND_WINDOWS_IDENTITY "Acme.Alerts")'),
          contains('set(BRAND_WINDOWS_PUBLISHER "CN=Acme Corp")'),
        ),
      );
    });

    test(
      'Linux: desktop entry, metainfo and icons are named after the application ID',
      () {
        expect(
          text(acme, 'linux/brand.cmake'),
          contains('set(BRAND_APPLICATION_ID "com.acme.Alerts")'),
        );
        expect(
          text(acme, 'linux/brand/com.acme.Alerts.desktop'),
          allOf(
            contains('Name=Acme Alerts\n'),
            contains('Exec=alerts %u\n'),
            contains('Icon=com.acme.Alerts\n'),
            contains('StartupWMClass=com.acme.Alerts\n'),
            contains('MimeType=x-scheme-handler/acme-alerts;\n'),
          ),
        );
        final metainfo = text(acme, 'linux/brand/com.acme.Alerts.metainfo.xml');
        expect(
          metainfo,
          allOf(
            contains('<id>com.acme.Alerts</id>'),
            contains(
              '<launchable type="desktop-id">com.acme.Alerts.desktop</launchable>',
            ),
            contains('<url type="homepage">https://acme.example</url>'),
          ),
        );
        expect(
          metainfo,
          isNot(contains('vcs-browser')),
          reason: 'empty links are left out',
        );
        for (final size in [16, 32, 48, 64, 128, 256, 512]) {
          expect(
            pngSize(
              acme.files['linux/brand/icons/hicolor/${size}x$size/apps/com.acme.Alerts.png']!,
            ),
            (size, size),
          );
        }
      },
    );

    test('tray and logo images', () {
      expect(pngSize(acme.files['assets/brand/logo.png']!), (512, 512));
      expect(pngSize(acme.files['assets/brand/tray_linux.png']!), (64, 64));
      final template = decodePng(
        acme.files['assets/brand/tray_macos_template.png']!,
      )!;
      expect((template.width, template.height), (44, 44));
      for (var i = 0; i < template.pixels.length; i += 4) {
        if (template.pixels[i + 3] > 0) {
          expect(template.pixels.sublist(i, i + 3), [0, 0, 0]);
        }
      }
    });

    test('store metadata comes from the brand, and omits what is not set', () {
      expect(
        text(acme, 'fastlane/metadata/android/en-US/title.txt'),
        'Acme Alerts\n',
      );
      expect(
        text(acme, 'fastlane/metadata/android/en-US/short_description.txt'),
        "Alerts from Acme's servers, on every device.\n",
      );
      expect(
        text(acme, 'fastlane/metadata/en-US/privacy_url.txt'),
        'https://acme.example/privacy\n',
      );
      expect(
        flare.files,
        isNot(contains('fastlane/metadata/en-US/privacy_url.txt')),
        reason: 'Flare has none yet',
      );
    });

    test('the Dart constants hold the brand values', () {
      expect(
        text(acme, 'lib/brand/brand.g.dart'),
        allOf(
          contains("static const String name = 'Acme Alerts';"),
          contains("static const String shortName = 'Acme';"),
          contains('static const int primaryColor = 0xFF2563EB;'),
          contains('static const bool allowCustomServers = false;'),
          contains(
            "static const String defaultServerUrl = 'https://alerts.acme.example';",
          ),
          contains(
            "static const String androidApplicationId = 'com.acme.alerts';",
          ),
          contains(
            "static const String appleAppGroup = 'group.com.acme.alerts';",
          ),
        ),
      );
    });
  });

  group('rebranding leaves no trace of the original', () {
    test(
      "no generated file mentions Flare, except the upstream attribution constants",
      () {
        for (final entry in acme.files.entries) {
          final path = entry.key;
          final isText = RegExp(
            r'\.(dart|properties|xml|xcconfig|cmake|desktop|json|txt)$',
          ).hasMatch(path);
          if (!isText) continue;
          final lines = utf8.decode(entry.value).split('\n');
          for (final line in lines.where(
            (l) => l.toLowerCase().contains('flare'),
          )) {
            expect(
              path,
              'lib/brand/brand.g.dart',
              reason: 'unexpected "flare" in $path: $line',
            );
            expect(
              line,
              anyOf(contains('upstreamName'), contains('upstreamUrl')),
            );
          }
        }
      },
    );

    test('file names carry the new identifiers only', () {
      expect(
        acme.files.keys.where((k) => k.toLowerCase().contains('flare')),
        isEmpty,
      );
      expect(
        flare.files.keys.where((k) => k.contains('com.example.Flare')),
        isNotEmpty,
      );
    });

    test(
      'the attribution constants are present in every build and cannot be configured',
      () {
        for (final out in [flare, acme]) {
          expect(
            text(out, 'lib/brand/brand.g.dart'),
            allOf(
              contains("upstreamName = 'Flare'"),
              contains(
                "upstreamUrl = 'https://github.com/vignesh-ramsh/flare'",
              ),
            ),
          );
        }
        final dir = copyBrand(
          acmeBrand,
          edits: [swap('schema: 1', 'schema: 1\nupstream_name: Mine')],
        );
        expect(load(dir).$2.errors, [contains('upstream_name: unknown key')]);
      },
    );
  });

  group('writing to the app folder', () {
    test(
      'switching brands removes everything the previous brand generated',
      () {
        final app = tempDir();
        writeOutputs(app, flare);
        expect(
          File(
            p.join(app.path, 'linux/brand/com.example.Flare.desktop'),
          ).existsSync(),
          isTrue,
        );

        writeOutputs(app, acme);
        expect(
          File(
            p.join(app.path, 'linux/brand/com.example.Flare.desktop'),
          ).existsSync(),
          isFalse,
        );
        expect(
          File(
            p.join(app.path, 'linux/brand/com.acme.Alerts.desktop'),
          ).existsSync(),
          isTrue,
        );
        expect(
          File(
            p.join(app.path, 'fastlane/metadata/en-US/privacy_url.txt'),
          ).existsSync(),
          isTrue,
        );

        writeOutputs(app, flare);
        expect(
          File(
            p.join(app.path, 'fastlane/metadata/en-US/privacy_url.txt'),
          ).existsSync(),
          isFalse,
          reason: 'a file the new brand does not produce must not linger',
        );
        expect(
          File(
            p.join(app.path, 'linux/brand/com.acme.Alerts.desktop'),
          ).existsSync(),
          isFalse,
        );
      },
    );

    test('google-services.json follows the brand that has one', () {
      final dir = copyBrand(
        acmeBrand,
        edits: [swap('android_config: ""', 'android_config: gs.json')],
      );
      File(p.join(dir.path, 'gs.json')).writeAsStringSync(
        jsonEncode({
          'client': [
            {
              'client_info': {
                'android_client_info': {'package_name': 'com.acme.alerts'},
              },
            },
          ],
        }),
      );
      final app = tempDir();
      writeOutputs(app, renderBrand(loadValid(dir)));
      expect(
        File(p.join(app.path, 'android/app/google-services.json')).existsSync(),
        isTrue,
      );

      writeOutputs(app, acme);
      expect(
        File(p.join(app.path, 'android/app/google-services.json')).existsSync(),
        isFalse,
      );
    });

    test("files the generator does not own are never touched", () {
      final app = tempDir();
      final mine = {
        'lib/main.dart': 'void main() {}',
        'lib/brand/notes.dart': '// hand-written, next to the generated file',
        'android/app/build.gradle.kts': 'plugins {}',
        'ios/Runner/Info.plist': '<plist/>',
        'ios/Runner/Assets.xcassets/LaunchImage.imageset/Contents.json': '{}',
        'fastlane/metadata/en-US/release_notes.txt': 'Fixed things',
        'fastlane/metadata/en-US/description.txt':
            'A long hand-written description',
      };
      for (final MapEntry(key: path, value: content) in mine.entries) {
        File(p.join(app.path, path))
          ..parent.createSync(recursive: true)
          ..writeAsStringSync(content);
      }
      writeOutputs(app, flare);
      writeOutputs(app, acme);
      for (final MapEntry(key: path, value: content) in mine.entries) {
        expect(
          File(p.join(app.path, path)).readAsStringSync(),
          content,
          reason: path,
        );
      }
    });

    test('a symlinked generated folder is unlinked, not followed', () {
      final app = tempDir();
      final precious = tempDir();
      File(p.join(precious.path, 'keep.txt')).writeAsStringSync('keep me');
      Directory(p.join(app.path, 'assets')).createSync();
      Link(p.join(app.path, 'assets/brand')).createSync(precious.path);
      writeOutputs(app, flare);
      expect(
        File(p.join(precious.path, 'keep.txt')).readAsStringSync(),
        'keep me',
      );
      expect(
        File(p.join(app.path, 'assets/brand/logo.png')).existsSync(),
        isTrue,
      );
      expect(
        Link(p.join(app.path, 'assets/brand')).existsSync(),
        isFalse,
        reason: 'replaced by a real folder',
      );
    });
  });

  group('awkward names', () {
    late Outputs awkward;
    setUpAll(() {
      final dir = copyBrand(
        acmeBrand,
        edits: [
          swap('name: Acme Alerts', 'name: "Café Bob\'s & Co."'),
          swap('short_name: Acme', 'short_name: "Café Bob\'s"'),
          swap(
            "description: Alerts from Acme's servers, on every device.",
            "description: \"Tom & Jerry's <alerts>, 100% réal.\"",
          ),
        ],
      );
      awkward = renderBrand(loadValid(dir));
    });

    test('XML metadata is escaped', () {
      final metainfo = text(
        awkward,
        'linux/brand/com.acme.Alerts.metainfo.xml',
      );
      expect(metainfo, contains('<name>Café Bob&apos;s &amp; Co.</name>'));
      expect(
        metainfo,
        contains('Tom &amp; Jerry&apos;s &lt;alerts&gt;, 100% réal.'),
      );
    });

    test('Java properties are ASCII, with other characters escaped', () {
      final props = text(awkward, 'android/brand.properties');
      expect(props.codeUnits.every((c) => c < 128), isTrue);
      final backslash = String.fromCharCode(0x5C);
      expect(props, contains('appName=Caf${backslash}u00e9 Bob'));
    });

    test('Dart string literals are escaped', () {
      final dart = text(awkward, 'lib/brand/brand.g.dart');
      expect(
        dart,
        contains(r"static const String name = 'Café Bob\'s & Co.';"),
      );
    });

    test('every generated Dart file passes the analyzer', () {
      final dir = tempDir();
      final files = <String>[];
      for (final (label, out) in [
        ('flare', flare),
        ('acme', acme),
        ('awkward', awkward),
      ]) {
        final file = File(p.join(dir.path, label, 'brand.g.dart'))
          ..createSync(recursive: true);
        file.writeAsBytesSync(out.files['lib/brand/brand.g.dart']!);
        files.add(file.path);
      }
      final result = Process.runSync(Platform.resolvedExecutable, [
        'analyze',
        '--fatal-infos',
        ...files,
      ]);
      expect(result.exitCode, 0, reason: '${result.stdout}${result.stderr}');
    });
  });

  group('keeping the program name in step', () {
    const linuxCmake = '''
cmake_minimum_required(VERSION 3.13)
# comment that mentions set(BINARY_NAME "x")
set(BINARY_NAME "flare")
set(APPLICATION_ID "com.example.Flare")
''';

    test('rewrites the BINARY_NAME line and nothing else', () {
      expect(
        pinBinaryName(linuxCmake, 'acme'),
        linuxCmake.replaceFirst('"flare"', '"acme"'),
      );
    });

    test('leaves a line that is already right as it is', () {
      expect(pinBinaryName(linuxCmake, 'flare'), linuxCmake);
    });

    test('copes with spacing, as Flutter\'s own tool does', () {
      expect(
        pinBinaryName('  set(BINARY_NAME   "old"  )\n', 'new'),
        '  set(BINARY_NAME   "new"  )\n',
      );
    });

    test('says there is no such line', () {
      expect(pinBinaryName('project(runner)\n', 'acme'), isNull);
      expect(pinBinaryName('# set(BINARY_NAME "flare")\n', 'acme'), isNull);
    });

    test(
      'is written into the desktop projects when the brand is generated',
      () {
        final app = tempDir();
        for (final platform in ['linux', 'windows']) {
          File(p.join(app.path, platform, 'CMakeLists.txt'))
            ..createSync(recursive: true)
            ..writeAsStringSync(linuxCmake);
        }

        writeOutputs(app, acme);

        for (final platform in ['linux', 'windows']) {
          final cmake = File(
            p.join(app.path, platform, 'CMakeLists.txt'),
          ).readAsStringSync();
          final binary = RegExp(
            r'set\(BRAND_BINARY_NAME "(.*)"\)',
          ).firstMatch(text(acme, '$platform/brand.cmake'))!.group(1);
          expect(
            cmake,
            contains('set(BINARY_NAME "$binary")'),
            reason: platform,
          );
          expect(cmake, contains('set(APPLICATION_ID "com.example.Flare")'));
        }
      },
    );

    test(
      'a second run changes nothing, and switching brands switches the name',
      () {
        final app = tempDir();
        final cmake = File(p.join(app.path, 'linux', 'CMakeLists.txt'))
          ..createSync(recursive: true)
          ..writeAsStringSync(linuxCmake);

        writeOutputs(app, flare);
        final first = cmake.readAsStringSync();
        writeOutputs(app, flare);
        expect(cmake.readAsStringSync(), first);

        writeOutputs(app, acme);
        expect(cmake.readAsStringSync(), isNot(first));
        writeOutputs(app, flare);
        expect(cmake.readAsStringSync(), first);
      },
    );

    test('is skipped where a platform folder does not exist', () {
      final app = tempDir();
      writeOutputs(
        app,
        acme,
      ); // no CMakeLists.txt anywhere: nothing to pin, nothing fails
      expect(
        File(p.join(app.path, 'linux', 'CMakeLists.txt')).existsSync(),
        isFalse,
      );
    });

    test(
      'stops when a desktop project has no BINARY_NAME line to keep in step',
      () {
        final app = tempDir();
        File(p.join(app.path, 'linux', 'CMakeLists.txt'))
          ..createSync(recursive: true)
          ..writeAsStringSync('project(runner)\n');

        expect(() => writeOutputs(app, acme), throwsStateError);
      },
    );
  });
}
