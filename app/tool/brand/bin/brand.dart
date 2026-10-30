import 'dart:io';

import 'package:args/args.dart';
import 'package:brand_tool/brand_tool.dart';
import 'package:path/path.dart' as p;

const _usage = '''
Validates a brand folder and generates the app's per-platform branding files.

Usage: dart run bin/brand.dart --brand <brand folder> --app <app folder> [options]
''';

Never _fail(String message, ArgParser parser, {int code = 64}) {
  stderr
    ..writeln(message)
    ..writeln()
    ..writeln(_usage)
    ..writeln(parser.usage);
  exit(code);
}

void main(List<String> arguments) {
  final parser = ArgParser()
    ..addOption(
      'brand',
      abbr: 'b',
      help: 'Folder with brand.yaml and the brand images.',
      valueHelp: 'dir',
    )
    ..addOption(
      'app',
      abbr: 'a',
      help: 'The Flutter app folder to generate into.',
      valueHelp: 'dir',
    )
    ..addOption(
      'upstream',
      help:
          "The project's own brand folder, to spot reused identities. Default: <app>/../brand.",
      valueHelp: 'dir',
    )
    ..addFlag(
      'release',
      help:
          'Refuse placeholder identifiers and URLs and a missing privacy policy.',
      negatable: false,
    )
    ..addFlag('help', abbr: 'h', negatable: false, help: 'Show this help.');

  final ArgResults args;
  try {
    args = parser.parse(arguments);
  } on FormatException catch (e) {
    _fail(e.message, parser);
  }
  if (args.flag('help')) {
    stdout
      ..writeln(_usage)
      ..writeln(parser.usage);
    return;
  }
  final brandPath = args.option('brand');
  final appPath = args.option('app');
  if (brandPath == null || appPath == null) {
    _fail('Both --brand and --app are required.', parser);
  }

  final brandDir = Directory(brandPath);
  final appDir = Directory(appPath);
  final upstream = Directory(
    args.option('upstream') ?? p.join(appPath, '..', 'brand'),
  );
  if (!appDir.existsSync()) _fail('App folder not found: $appPath', parser);

  final report = Report();
  final brand = loadBrand(
    brandDir,
    report,
    release: args.flag('release'),
    upstream: upstream,
  );

  for (final warning in report.warnings) {
    stderr.writeln('warning: $warning');
  }
  if (brand == null) {
    for (final error in report.errors) {
      stderr.writeln('error: $error');
    }
    stderr.writeln(
      '\nNothing was generated: fix the ${report.errors.length} problem(s) above.',
    );
    exit(65);
  }

  final outputs = renderBrand(brand);
  writeOutputs(appDir, outputs);

  final spec = brand.spec;
  stdout
    ..writeln('Brand "${spec.name}" (${spec.androidId})')
    ..writeln(
      'Generated ${outputs.files.length} files in ${p.normalize(appDir.absolute.path)}',
    )
    ..writeln(
      brand.googleServices == null
          ? 'No Firebase config: this build has no FCM push on Android.'
          : 'Firebase config: included.',
    );
}
