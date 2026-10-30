import 'dart:io';

import 'package:brand_tool/brand_tool.dart';
import 'package:path/path.dart' as p;
import 'package:test/test.dart';

import 'support.dart';

String get baseYaml =>
    File(p.join(flareBrand.path, 'brand.yaml')).readAsStringSync();

Report parse(String yaml) {
  final report = Report();
  parseBrandYaml(yaml, report);
  return report;
}

void main() {
  test('the official brand and the example brand are valid', () {
    for (final dir in [flareBrand, acmeBrand]) {
      final report = parse(
        File(p.join(dir.path, 'brand.yaml')).readAsStringSync(),
      );
      expect(report.errors, isEmpty, reason: dir.path);
      expect(report.warnings, isEmpty, reason: dir.path);
    }
  });

  test('values are read as written', () {
    final spec = parseBrandYaml(baseYaml, Report())!;
    expect(spec.name, 'Flare');
    expect(spec.urlScheme, 'flare');
    expect(spec.androidId, 'com.example.flare');
    expect(spec.primaryColor.argb, 0xFFF97316);
    expect(spec.iconBackground.hex, '#0F172A');
    expect(spec.allowCustomServers, isTrue);
    expect(
      spec.binaryName,
      'flare',
      reason: 'last part of the Linux ID, lower case',
    );
    expect(spec.appleExtensionId, 'com.example.flare.NotificationService');
    expect(spec.appleAppGroup, 'group.com.example.flare');
  });

  test('short_name defaults to name when it is short enough', () {
    final spec = parseBrandYaml(
      swap('short_name: Flare ', '# short_name: Flare ')(baseYaml),
      Report(),
    )!;
    expect(spec.shortName, 'Flare');
  });

  test('a trailing slash on the server URL is dropped', () {
    final spec = parseBrandYaml(
      swap('server_url: ""', 'server_url: https://flare.test/')(baseYaml),
      Report(),
    )!;
    expect(spec.serverUrl, 'https://flare.test');
  });

  test('plain http is accepted for local development servers only', () {
    final local = parseBrandYaml(
      swap('server_url: ""', 'server_url: http://10.0.2.2:8080')(baseYaml),
      Report(),
    );
    expect(local, isNotNull);
    expect(
      parse(
        swap('server_url: ""', 'server_url: http://example.org')(baseYaml),
      ).errors,
      contains(contains('must use https')),
    );
  });

  test('unusual but legal names are accepted', () {
    for (final name in [
      '"Café Ünï"',
      '"Bob\'s & Co."',
      '"ファイア"',
      '"Alerts 24/7"'.replaceAll('/', '-'),
    ]) {
      final report = parse(swap('name: Flare ', 'name: $name ')(baseYaml));
      expect(report.errors, isEmpty, reason: name);
    }
  });

  group('rejects', () {
    // (label, text to replace, replacement, expected message)
    final cases = <(String, String, String, String)>[
      (
        'a name over 30 characters',
        'name: Flare ',
        'name: ${'A' * 31} ',
        'at most 30',
      ),
      (
        'a name with a slash',
        'name: Flare ',
        'name: "Fla/re" ',
        'may only contain',
      ),
      (
        'a short name over 12 characters',
        'short_name: Flare ',
        'short_name: "A very long name" ',
        'limit is 12',
      ),
      (
        'a description over 80 characters',
        'description: Push',
        'description: ${'x' * 80} Push',
        'at most 80',
      ),
      (
        'a description with quotes',
        'description: Push notifications for your scripts, servers and apps.',
        "description: 'Say \"hi\"'",
        'must not contain',
      ),
      (
        'an upper-case URL scheme',
        'url_scheme: flare ',
        'url_scheme: Flare ',
        'lowercase',
      ),
      (
        'a reserved URL scheme',
        'url_scheme: flare ',
        'url_scheme: https ',
        'belongs to the operating system',
      ),
      (
        'an app links domain with https://',
        'app_links_domain: ""',
        'app_links_domain: "https://x.org/"',
        'host name',
      ),
      (
        'an Android ID with one part',
        'android: com.example.flare ',
        'android: flare ',
        'at least two parts',
      ),
      (
        'an Android ID part starting with a digit',
        'android: com.example.flare ',
        'android: com.1bad.app ',
        'starting with a letter',
      ),
      (
        'an Apple ID with an underscore',
        'apple: com.example.flare ',
        'apple: com.example.my_app ',
        'ids.apple',
      ),
      (
        'a short Apple Team ID',
        'apple_team_id: ""',
        'apple_team_id: "abc"',
        '10 letters and digits',
      ),
      (
        'a numeric Apple Team ID',
        'apple_team_id: ""',
        'apple_team_id: 1234567890',
        'put it in quotes',
      ),
      (
        'a Windows ID that is too short',
        'windows: Example.Flare',
        'windows: A',
        'MSIX',
      ),
      (
        'a Windows publisher without CN=',
        'windows_publisher: "CN=Example"',
        'windows_publisher: "Example"',
        'signing certificate',
      ),
      (
        'a Linux ID with one part',
        'linux: com.example.Flare',
        'linux: Flare',
        'at least two parts',
      ),
      (
        'an unquoted colour (YAML makes it a comment)',
        'primary_color: "#F97316"',
        'primary_color: #F97316',
        'must be quoted',
      ),
      (
        'a short colour',
        'primary_color: "#F97316"',
        'primary_color: "#F97"',
        'must be a colour',
      ),
      (
        'an http server URL',
        'server_url: ""',
        'server_url: http://flare.test',
        'must use https',
      ),
      (
        'a server URL with a query',
        'server_url: ""',
        'server_url: https://flare.test/?x=1',
        'query',
      ),
      (
        'a server URL with credentials',
        'server_url: ""',
        'server_url: https://user:pw@flare.test',
        'user name',
      ),
      (
        'a link without a scheme',
        'website: https://github.com/vignesh-ramsh/flare',
        'website: github.com',
        'full URL',
      ),
      (
        'a locked app with no server',
        'allow_custom_servers: true',
        'allow_custom_servers: false',
        'no server to connect to',
      ),
      (
        'a quoted boolean',
        'allow_custom_servers: true',
        'allow_custom_servers: "yes"',
        'true or false',
      ),
      ('an unknown schema', 'schema: 1', 'schema: 2', 'understands schema 1'),
      (
        'an unknown top-level key',
        'schema: 1',
        'schema: 1\nextra: true',
        'extra: unknown key',
      ),
      (
        'a misspelled key',
        'url_scheme: flare',
        'url_sheme: flare',
        'app.url_sheme: unknown key',
      ),
      (
        'a missing required key (via the same typo)',
        'url_scheme: flare',
        'url_sheme: flare',
        'app.url_scheme: is required',
      ),
    ];
    for (final (label, from, to, message) in cases) {
      test(label, () {
        expect(
          parse(swap(from, to)(baseYaml)).errors,
          contains(contains(message)),
        );
      });
    }
  });

  test('every problem is reported at once, not just the first', () {
    final yaml = [
      swap('url_scheme: flare ', 'url_scheme: Flare '),
      swap('android: com.example.flare ', 'android: flare '),
      swap('primary_color: "#F97316"', 'primary_color: "red"'),
    ].fold(baseYaml, (text, edit) => edit(text));
    final errors = parse(yaml).errors;
    expect(errors, hasLength(3));
    expect(
      errors,
      containsAll([
        contains('app.url_scheme'),
        contains('ids.android'),
        contains('look.primary_color'),
      ]),
    );
  });

  test('a short name is required when the name is too long to reuse', () {
    final yaml = [
      swap('name: Flare ', 'name: Long App Name Here '),
      swap('short_name: Flare ', '# short_name: Flare '),
    ].fold(baseYaml, (text, edit) => edit(text));
    expect(parse(yaml).errors, contains(contains('set a short_name')));
  });

  test('missing sections are named', () {
    final errors = parse('schema: 1\n').errors;
    expect(
      errors,
      containsAll([
        contains('app: section is missing'),
        contains('ids: section is missing'),
      ]),
    );
  });

  test('broken YAML and non-mappings are reported, not thrown', () {
    expect(parse('name: [unclosed').errors, [contains('brand.yaml')]);
    expect(parse('- a\n- b\n').errors, [contains('must contain a mapping')]);
    expect(parse('').errors, [contains('must contain a mapping')]);
  });
}
