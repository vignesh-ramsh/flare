import 'dart:math';

import 'package:app/data/server_url.dart';
import 'package:app/data/subscribe_link.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  SubscribeLink? parse(String text) =>
      SubscribeLink.parse(text, scheme: 'acme');

  group('reading a subscribe link', () {
    test('takes the server and the topic', () {
      final link = parse('acme://subscribe/example.com/alerts')!;
      expect(link.server.host, 'example.com');
      expect(link.server.url, 'https://example.com');
      expect(link.topic, 'alerts');
      expect(link.plainHttp, isFalse);
    });

    test('keeps a port, and says plain http when the link does', () {
      final link = parse(
        'acme://subscribe/192.168.1.5:8080/alerts?scheme=http',
      )!;
      expect(link.server.host, '192.168.1.5:8080');
      expect(link.server.url, 'http://192.168.1.5:8080');
      expect(link.plainHttp, isTrue);
    });

    test('reads an encoded port, as the routes write it', () {
      final link = parse('acme://subscribe/example.com%3A8443/alerts')!;
      expect(link.server.host, 'example.com:8443');
    });

    test('reads the scheme and the word subscribe in any case', () {
      expect(
        parse('ACME://Subscribe/Example.COM/alerts')!.server.host,
        'example.com',
      );
    });

    test('reads the other shapes platforms hand over', () {
      expect(parse('acme:///subscribe/example.com/alerts')?.topic, 'alerts');
      expect(parse('acme:subscribe/example.com/alerts')?.topic, 'alerts');
    });

    test('ignores surrounding space', () {
      expect(parse('  acme://subscribe/example.com/alerts\n')?.topic, 'alerts');
    });

    test('refuses a topic the server would not accept', () {
      expect(parse('acme://subscribe/example.com/bad%20name'), isNull);
      expect(parse('acme://subscribe/example.com/${'x' * 65}'), isNull);
      expect(parse('acme://subscribe/example.com/a.b'), isNull);
    });

    test('refuses a host that is not one', () {
      expect(parse('acme://subscribe/-bad-/alerts'), isNull);
      expect(parse('acme://subscribe/user@example.com/alerts'), isNull);
    });

    test('refuses what is not a subscribe link', () {
      for (final text in [
        '',
        'alerts',
        'example.com/alerts',
        'acme://settings/example.com/alerts',
        'acme://subscribe/example.com',
        'acme://subscribe/example.com/alerts/more',
        'other://subscribe/example.com/alerts',
        'mailto:someone@example.com',
        'acme://subscribe/example.com/alerts and more',
      ]) {
        expect(parse(text), isNull, reason: text);
      }
    });
  });

  group('reading a web address of a topic', () {
    test('takes the server and the topic', () {
      final link = parse('https://ntfy.example.com/alerts')!;
      expect(link.server.url, 'https://ntfy.example.com');
      expect(link.topic, 'alerts');
    });

    test('keeps plain http and a port', () {
      final link = parse('http://10.0.0.2:8080/alerts')!;
      expect(link.server.url, 'http://10.0.0.2:8080');
      expect(link.plainHttp, isTrue);
    });

    test('accepts a trailing slash', () {
      expect(parse('https://example.com/alerts/')?.topic, 'alerts');
    });

    test('refuses an address that is not a topic', () {
      for (final text in [
        'https://example.com',
        'https://example.com/',
        'https://example.com/a/b',
        'https://example.com/alerts?x=1',
        'https://example.com/alerts#top',
        'https://example.com/not valid',
        'ftp://example.com/alerts',
      ]) {
        expect(parse(text), isNull, reason: text);
      }
    });
  });

  group('writing a subscribe link', () {
    test('writes what it reads', () {
      for (final text in [
        'acme://subscribe/example.com/alerts',
        'acme://subscribe/example.com:8443/alerts',
        'acme://subscribe/192.168.1.5:8080/alerts?scheme=http',
      ]) {
        final link = parse(text)!;
        expect(link.format(scheme: 'acme'), text);
        expect(parse(link.format(scheme: 'acme')), link);
      }
    });

    test('leaves the usual ports out', () {
      expect(
        parse('https://example.com:443/a')!.format(scheme: 'acme'),
        'acme://subscribe/example.com/a',
      );
    });

    test('uses the brand\'s scheme by default', () {
      expect(
        parse('https://example.com/a')!.format(),
        startsWith('flare://subscribe/'),
      );
    });

    test('has a route that opens the form, with the port encoded', () {
      expect(
        parse('acme://subscribe/example.com:8443/alerts')!.location,
        '/subscribe/example.com%3A8443/alerts',
      );
      expect(
        parse('http://10.0.0.2:8080/alerts')!.location,
        '/subscribe/10.0.0.2%3A8080/alerts?scheme=http',
      );
    });

    test('has the address a script publishes to', () {
      expect(
        parse('acme://subscribe/example.com/alerts')!.webAddress,
        'https://example.com/alerts',
      );
    });
  });

  test('a link made for the route reads back as the same topic', () {
    final link = parse('acme://subscribe/example.com:8443/alerts')!;
    final route = Uri.parse(link.location);
    final again = SubscribeLink.parse(
      'acme://subscribe/${route.pathSegments[1]}/${route.pathSegments[2]}',
      scheme: 'acme',
    );
    expect(again, link);
  });

  group('a random topic name', () {
    test('is 16 lower-case letters and digits, and valid', () {
      final random = Random(1);
      for (var i = 0; i < 200; i++) {
        final name = randomTopicName(random);
        expect(name, matches(RegExp(r'^[a-z0-9]{16}$')));
        expect(isValidTopic(name), isTrue);
      }
    });

    test('differs every time', () {
      final random = Random.secure();
      expect({
        for (var i = 0; i < 500; i++) randomTopicName(random),
      }, hasLength(500));
    });

    test('can be any length', () {
      expect(randomTopicName(Random(2), length: 5), hasLength(5));
    });
  });
}
