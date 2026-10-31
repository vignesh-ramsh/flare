import 'package:app/data/server_url.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('ServerAddress.parse', () {
    ({String host, String url})? parse(String input) {
      final a = ServerAddress.parse(input);
      return a == null ? null : (host: a.host, url: a.url);
    }

    test('reads a full address', () {
      expect(parse('https://example.com'), (
        host: 'example.com',
        url: 'https://example.com',
      ));
    });

    test('assumes https when there is no scheme', () {
      expect(parse('example.com'), (
        host: 'example.com',
        url: 'https://example.com',
      ));
    });

    test('trims spaces and the trailing slash', () {
      expect(parse('  https://example.com/  '), (
        host: 'example.com',
        url: 'https://example.com',
      ));
    });

    test('lowercases the host', () {
      expect(parse('HTTPS://Example.COM'), (
        host: 'example.com',
        url: 'https://example.com',
      ));
    });

    test('keeps a port, but not the default one', () {
      expect(parse('http://localhost:8080'), (
        host: 'localhost:8080',
        url: 'http://localhost:8080',
      ));
      expect(parse('https://example.com:443'), (
        host: 'example.com',
        url: 'https://example.com',
      ));
      expect(parse('http://example.com:80'), (
        host: 'example.com',
        url: 'http://example.com',
      ));
      expect(parse('https://example.com:80'), (
        host: 'example.com:80',
        url: 'https://example.com:80',
      ));
    });

    test('keeps a path, for servers behind a prefix', () {
      expect(parse('https://example.com/notify/'), (
        host: 'example.com',
        url: 'https://example.com/notify',
      ));
    });

    test('reads IPv6 literals', () {
      expect(parse('http://[::1]:8080'), (
        host: '[::1]:8080',
        url: 'http://[::1]:8080',
      ));
    });

    test('rejects what is not a plain web address', () {
      for (final input in [
        '',
        '   ',
        'ftp://example.com',
        'https://',
        'https://user:pass@example.com',
        'https://example.com?x=1',
        'https://example.com#frag',
        'not a url',
        'exa mple.com',
        'example.com/a b',
        'https://exa_mple!.com',
        'https://-bad.com',
      ]) {
        expect(ServerAddress.parse(input), isNull, reason: input);
      }
    });
  });

  group('isValidTopic', () {
    test('accepts letters, digits, dashes and underscores up to 64', () {
      for (final name in ['a', 'alerts', 'Alerts-2_x', 'A' * 64]) {
        expect(isValidTopic(name), isTrue, reason: name);
      }
    });

    test('rejects the rest', () {
      for (final name in [
        '',
        'A' * 65,
        'has space',
        'a/b',
        'a.b',
        'é',
        'a\n',
      ]) {
        expect(isValidTopic(name), isFalse, reason: name);
      }
    });
  });
}
