import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

/// The native projects must take the app's name and identifiers from the brand output
/// (`make brand`), never from text written in them, so a rebrand touches nothing else.
/// This reads the files, so it holds on every machine; CI builds each platform and checks
/// the result.
String read(String path) => File(path).readAsStringSync();

/// Flutter's tool finds the program to run (`flutter run -d linux`) by reading a literal
/// `set(BINARY_NAME "...")` line, which it cannot get from a variable. `make brand` writes
/// that line, so it must equal the brand's program name; CMake also refuses to build if not.
void expectProgramNamePinned(String cmake, String brandCmakePath) {
  // The same pattern Flutter's tool uses.
  final literal = RegExp(
    r'^\s*set\(BINARY_NAME\s*"(.*)"\s*\)\s*$',
    multiLine: true,
  ).firstMatch(cmake);
  expect(
    literal,
    isNotNull,
    reason: 'Flutter reads this line to find the program',
  );
  final brand = RegExp(r'set\(BRAND_BINARY_NAME "(.*)"\)')
      .firstMatch(read(brandCmakePath))!
      .group(1);
  expect(literal!.group(1), brand, reason: 'run make brand');
  expect(cmake, contains('BINARY_NAME STREQUAL BRAND_BINARY_NAME'));
}

void main() {
  group('Android', () {
    final gradle = read('android/app/build.gradle.kts');
    final manifest = read('android/app/src/main/AndroidManifest.xml');

    test('takes its ID, label and URL scheme from brand.properties', () {
      expect(gradle, contains('rootProject.file("brand.properties")'));
      expect(
        gradle,
        contains('applicationId = brand.getProperty("applicationId")'),
      );
      expect(
        gradle,
        contains(
          'resValue("string", "app_name", brand.getProperty("appName"))',
        ),
      );
      expect(
        gradle,
        contains(
          'manifestPlaceholders["urlScheme"] = brand.getProperty("urlScheme")',
        ),
      );
      expect(gradle, contains('res.srcDir("src/brand/res")'));
      expect(
        gradle,
        contains('resValues = true'),
        reason: 'resValue() fails without it',
      );
    });

    test('does not write the ID or the label in the build', () {
      expect(gradle, isNot(contains('applicationId = "')));
      expect(manifest, contains('android:label="@string/app_name"'));
      expect(manifest, isNot(contains('android:label="app"')));
    });

    test('can reach the network in release builds', () {
      expect(manifest, contains('android.permission.INTERNET'));
    });

    test('allows plain http, as self-hosters on a home network often have no certificate', () {
      expect(manifest, contains('android:usesCleartextTraffic="true"'));
    });

    test('opens subscribe links, with the scheme from the brand', () {
      // <scheme>://subscribe/<host>/<topic>; the manifest never spells the scheme itself.
      expect(
        manifest,
        contains(
          r'<data android:scheme="${urlScheme}" android:host="subscribe"/>',
        ),
      );
      expect(manifest, contains('android.intent.action.VIEW'));
      expect(manifest, contains('android.intent.category.BROWSABLE'));
      expect(
        manifest,
        contains('flutter_deeplinking_enabled'),
        reason: 'Flutter hands the link to the router only if asked',
      );
    });

    test(
      'core library desugaring is on, which the notifications plugin requires',
      () {
        expect(gradle, contains('isCoreLibraryDesugaringEnabled = true'));
        expect(
          gradle,
          contains(
            'coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:',
          ),
        );
      },
    );

    test(
      'has no launcher icons of its own, which would clash with the brand\'s',
      () {
        final res = Directory('android/app/src/main/res');
        final icons = res
            .listSync(recursive: true)
            .whereType<File>()
            .where(
              (f) =>
                  f.path.contains('mipmap') && f.path.contains('ic_launcher'),
            );
        expect(icons, isEmpty);
      },
    );
  });

  group('iOS', () {
    test('takes its bundle ID, team and name from Brand.xcconfig', () {
      for (final file in [
        'ios/Flutter/Debug.xcconfig',
        'ios/Flutter/Release.xcconfig',
      ]) {
        expect(read(file), contains('#include "Brand.xcconfig"'), reason: file);
      }
      final project = read('ios/Runner.xcodeproj/project.pbxproj');
      expect(
        project,
        contains(r'PRODUCT_BUNDLE_IDENTIFIER = "$(BRAND_BUNDLE_ID)";'),
      );
      expect(project, contains(r'DEVELOPMENT_TEAM = "$(BRAND_TEAM_ID)";'));
      expect(project, isNot(contains('com.example')));

      final plist = read('ios/Runner/Info.plist');
      expect(plist, contains(r'<string>$(BRAND_APP_NAME)</string>'));
      expect(plist, isNot(contains('<string>App</string>')));
    });

    test('allows plain http, as self-hosters on a home network often have no certificate', () {
      final plist = read('ios/Runner/Info.plist');
      expect(plist, contains('NSAllowsArbitraryLoads'));
      expect(plist, isNot(contains('NSAllowsLocalNetworking')));
    });

    test('opens subscribe links, with the scheme from Brand.xcconfig', () {
      final plist = read('ios/Runner/Info.plist');
      expect(plist, contains('<key>CFBundleURLTypes</key>'));
      expect(plist, contains(r'<string>$(BRAND_URL_SCHEME)</string>'));
      expect(plist, contains('<key>FlutterDeepLinkingEnabled</key>'));
      expect(
        read('ios/Flutter/Brand.xcconfig'),
        contains('BRAND_URL_SCHEME = '),
      );
    });
  });

  group('macOS', () {
    test('takes its name and bundle ID from Brand.xcconfig', () {
      final info = read('macos/Runner/Configs/AppInfo.xcconfig');
      expect(info, contains('#include "Brand.xcconfig"'));
      expect(info, contains(r'PRODUCT_NAME = $(BRAND_APP_NAME)'));
      expect(info, contains(r'PRODUCT_BUNDLE_IDENTIFIER = $(BRAND_BUNDLE_ID)'));
      expect(info, isNot(contains('com.example.app')));
    });

    test('lets the sandboxed app make outgoing connections', () {
      for (final file in [
        'macos/Runner/Release.entitlements',
        'macos/Runner/DebugProfile.entitlements',
      ]) {
        expect(
          read(file),
          contains('com.apple.security.network.client'),
          reason: file,
        );
      }
    });
  });

  group('Windows', () {
    test('takes its program name and title from brand.cmake', () {
      final cmake = read('windows/CMakeLists.txt');
      expect(
        cmake,
        contains(r'include("${CMAKE_CURRENT_SOURCE_DIR}/brand.cmake")'),
      );
      expectProgramNamePinned(cmake, 'windows/brand.cmake');

      expect(read('windows/runner/main.cpp'), contains('L"" BRAND_APP_NAME'));
      final rc = read('windows/runner/Runner.rc');
      for (final field in [
        'FileDescription',
        'InternalName',
        'OriginalFilename',
        'ProductName',
      ]) {
        final line = rc.split('\n').firstWhere((l) => l.contains('"$field"'));
        expect(line, contains('BRAND_'), reason: field);
      }
    });
  });

  group('Linux', () {
    test('takes its program name, ID and title from brand.cmake', () {
      final cmake = read('linux/CMakeLists.txt');
      expect(
        cmake,
        contains(r'include("${CMAKE_CURRENT_SOURCE_DIR}/brand.cmake")'),
      );
      expectProgramNamePinned(cmake, 'linux/brand.cmake');
      expect(cmake, contains(r'set(APPLICATION_ID "${BRAND_APPLICATION_ID}")'));
      expect(
        read('linux/runner/my_application.cc'),
        contains('BRAND_APP_NAME'),
      );
      expect(read('linux/runner/my_application.cc'), isNot(contains('"app"')));
    });
  });
}
