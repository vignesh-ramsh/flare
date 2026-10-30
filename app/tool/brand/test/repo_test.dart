import 'dart:io';

import 'package:brand_tool/brand_tool.dart';
import 'package:path/path.dart' as p;
import 'package:test/test.dart';

import 'support.dart';

void main() {
  test('.gitignore lists every generated path, so none gets committed', () {
    final lines = File(
      p.join(repoRoot.path, '.gitignore'),
    ).readAsLinesSync().map((line) => line.trim()).toSet();
    for (final file in ownedFiles) {
      expect(
        lines,
        contains('/app/$file'),
        reason: '$file is generated but not ignored',
      );
    }
    for (final dir in ownedDirectories) {
      expect(
        lines,
        contains('/app/$dir/'),
        reason: '$dir is generated but not ignored',
      );
    }
  });

  group(
    'check_brand_neutral.sh',
    () {
      final script = File(
        p.join(repoRoot.path, 'app', 'tool', 'check_brand_neutral.sh'),
      );

      /// Runs the script against a throwaway app folder holding [files].
      ProcessResult check(Map<String, String> files) {
        final app = Directory(p.join(tempDir().path, 'app'))..createSync();
        Directory(p.join(app.path, 'tool')).createSync();
        final copy = script.copySync(
          p.join(app.path, 'tool', 'check_brand_neutral.sh'),
        );
        files.forEach((path, content) {
          File(p.join(app.path, path))
            ..parent.createSync(recursive: true)
            ..writeAsStringSync(content);
        });
        return Process.runSync('sh', [copy.path]);
      }

      test('passes when there is no app code yet', () {
        expect(check({}).exitCode, 0);
      });

      test('passes when code and translations use the brand', () {
        final result = check({
          'lib/main.dart': 'final title = Brand.name;',
          'l10n/app_en.arb': '{"about": "About {appName}"}',
        });
        expect(result.exitCode, 0, reason: '${result.stdout}${result.stderr}');
      });

      test('fails on the name in Dart code, and says where', () {
        final result = check({'lib/ui/home.dart': "Text('Welcome to Flare')"});
        expect(result.exitCode, 1);
        expect(result.stdout, contains('lib/ui/home.dart:1'));
        expect(result.stderr, contains('Brand.name'));
      });

      test('fails on the name in a translation', () {
        final result = check({'l10n/app_en.arb': '{"about": "About Flare"}'});
        expect(result.exitCode, 1);
      });

      test('lib/brand is exempt, because that is where the name lives', () {
        expect(
          check({
            'lib/brand/brand.g.dart': "static const upstreamName = 'Flare';",
          }).exitCode,
          0,
        );
      });

      test('a folder that is merely called brand elsewhere is not exempt', () {
        expect(
          check({'lib/features/brand/page.dart': "Text('Flare')"}).exitCode,
          1,
        );
      });

      test('only the capitalised name counts, not package names', () {
        expect(
          check({
            'lib/main.dart': "import 'package:flare_app/src/app.dart';",
          }).exitCode,
          0,
        );
      });
    },
    skip: Platform.isWindows ? 'needs a POSIX shell' : false,
  );
}
