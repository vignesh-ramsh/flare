import 'dart:async';
import 'dart:convert';

import 'package:app/net/api_client.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

void main() {
  final base = Uri.parse('https://example.com');

  ServerApi apiFor(
    Future<http.Response> Function(http.Request) handler, {
    Duration timeout = const Duration(seconds: 5),
  }) => ServerApi(client: MockClient(handler), timeout: timeout);

  http.Response json(int status, Object body) => http.Response(
    jsonEncode(body),
    status,
    headers: {'content-type': 'application/json'},
  );

  Future<void> expectFailure(
    Future<Object?> future,
    ApiFailure failure, {
    int? code,
  }) async {
    try {
      await future;
      fail('expected $failure');
    } on ApiException catch (e) {
      expect(e.failure, failure);
      if (code != null) expect(e.code, code);
    }
  }

  const config = {
    'api_version': 1,
    'name': 'Acme Alerts',
    'primary_color': '#F97316',
    'app_url_scheme': 'acme',
    'features': ['publish', 'websocket'],
    'limits': {'message_size': 4096, 'cache_duration': 86400},
    'auth': {'default_access': 'deny-all'},
  };

  group('apiUri', () {
    test(
      'adds the path after any prefix, and a trailing slash does not matter',
      () {
        expect(
          apiUri(Uri.parse('https://example.com'), '/v1/config').toString(),
          'https://example.com/v1/config',
        );
        expect(
          apiUri(
            Uri.parse('https://example.com/notify/'),
            '/v1/config',
          ).toString(),
          'https://example.com/notify/v1/config',
        );
        expect(
          apiUri(
            Uri.parse('http://localhost:8080'),
            '/v1/auth/login',
          ).toString(),
          'http://localhost:8080/v1/auth/login',
        );
      },
    );
  });

  group('config', () {
    test('reads what a server says about itself', () async {
      http.Request? seen;
      final api = apiFor((request) async {
        seen = request;
        return json(200, config);
      });
      final info = await api.config(Uri.parse('https://example.com/notify'));

      expect(seen!.method, 'GET');
      expect(seen!.url.toString(), 'https://example.com/notify/v1/config');
      expect(
        (info.name, info.apiVersion, info.defaultAccess),
        ('Acme Alerts', 1, 'deny-all'),
      );
      expect(info.features, ['publish', 'websocket']);
      expect(info.needsSignIn, isTrue);
    });

    test('knows which access modes need a sign-in', () async {
      for (final (access, needs) in [
        ('deny-all', true),
        ('write-only', true),
        ('read-only', false),
        ('read-write', false),
      ]) {
        final api = apiFor(
          (_) async => json(200, {
            ...config,
            'auth': {'default_access': access},
          }),
        );
        expect((await api.config(base)).needsSignIn, needs, reason: access);
      }
    });

    test('assumes open access when the server does not say', () async {
      final api = apiFor((_) async => json(200, {...config}..remove('auth')));
      expect((await api.config(base)).defaultAccess, 'read-write');
    });

    test('says the address is not a Flare server when it answers with something else', () async {
      for (final response in [
        http.Response('<html>Welcome</html>', 200),
        http.Response('not found', 404),
        json(200, {'hello': 'world'}),
        json(200, {...config, 'api_version': 'one'}),
        json(200, [1, 2]),
        http.Response('', 500),
      ]) {
        await expectFailure(
          apiFor((_) async => response).config(base),
          ApiFailure.notSupported,
        );
      }
    });

    test(
      'tells apart no answer, a slow answer and a bad certificate',
      () async {
        await expectFailure(
          apiFor((_) => throw http.ClientException('Connection refused'))
              .config(base),
          ApiFailure.unreachable,
        );
        await expectFailure(
          apiFor(
            (_) => throw http.ClientException(
              'HandshakeException: CERTIFICATE_VERIFY_FAILED',
            ),
          ).config(base),
          ApiFailure.certificate,
        );
        await expectFailure(
          apiFor(
            (_) => Completer<http.Response>().future,
            timeout: const Duration(milliseconds: 50),
          ).config(base),
          ApiFailure.timeout,
        );
      },
    );
  });

  group('login', () {
    const answer = {
      'id': 'tok_1',
      'created': 1700000000,
      'token': 'flr_secret',
      'user': 'alice',
      'role': 'admin',
    };

    test(
      'sends the password as Basic credentials and returns the token',
      () async {
        http.Request? seen;
        final api = apiFor((request) async {
          seen = request;
          return json(200, answer);
        });
        final result = await api.login(
          Uri.parse('http://192.168.1.5:8080'),
          user: 'alice',
          password: 'p:ss wörd',
          label: 'Phone',
        );

        expect(seen!.method, 'POST');
        expect(seen!.url.toString(), 'http://192.168.1.5:8080/v1/auth/login');
        final basic = seen!.headers['Authorization']!;
        expect(
          utf8.decode(base64.decode(basic.substring('Basic '.length))),
          'alice:p:ss wörd',
        );
        expect(jsonDecode(seen!.body), {'label': 'Phone'});
        expect(
          (result.token, result.tokenId, result.user, result.role),
          ('flr_secret', 'tok_1', 'alice', 'admin'),
        );
      },
    );

    test('sends no label when there is none', () async {
      http.Request? seen;
      await apiFor((request) async {
        seen = request;
        return json(200, answer);
      }).login(base, user: 'a', password: 'b');
      expect(jsonDecode(seen!.body), isEmpty);
    });

    test('says the password was wrong', () async {
      final api = apiFor(
        (_) async => json(401, {
          'code': 40102,
          'http': 401,
          'error': 'invalid user name or password',
        }),
      );
      await expectFailure(
        api.login(base, user: 'a', password: 'b'),
        ApiFailure.badCredentials,
        code: 40102,
      );
    });

    test('says there were too many attempts', () async {
      final api = apiFor(
        (_) async =>
            json(429, {'code': 42902, 'http': 429, 'error': 'slow down'}),
      );
      await expectFailure(
        api.login(base, user: 'a', password: 'b'),
        ApiFailure.throttled,
        code: 42902,
      );
    });

    test('reports a server error, and a reply it cannot read', () async {
      await expectFailure(
        apiFor(
          (_) async => json(500, {'code': 50001, 'http': 500, 'error': 'oops'}),
        ).login(base, user: 'a', password: 'b'),
        ApiFailure.server,
        code: 50001,
      );
      await expectFailure(
        apiFor((_) async => json(200, {'ok': true}))
            .login(base, user: 'a', password: 'b'),
        ApiFailure.server,
      );
      await expectFailure(
        apiFor((_) async => http.Response('<html>', 502))
            .login(base, user: 'a', password: 'b'),
        ApiFailure.server,
      );
    });

    test('reports an unreachable server', () async {
      await expectFailure(
        apiFor((_) => throw http.ClientException('no route'))
            .login(base, user: 'a', password: 'b'),
        ApiFailure.unreachable,
      );
    });
  });

  group('revokeToken', () {
    test('deletes the token by its id, signed with the token', () async {
      http.Request? seen;
      await apiFor((request) async {
        seen = request;
        return http.Response('', 204);
      }).revokeToken(base, token: 'flr_secret', tokenId: 'tok/1 2');

      expect(seen!.method, 'DELETE');
      expect(seen!.url.path, '/v1/account/tokens/tok%2F1%202');
      expect(seen!.headers['Authorization'], 'Bearer flr_secret');
    });

    test('counts a token that is already gone as done', () async {
      for (final status in [404, 401]) {
        await apiFor((_) async => http.Response('', status))
            .revokeToken(base, token: 't', tokenId: 'id');
      }
    });

    test('reports other failures', () async {
      await expectFailure(
        apiFor(
          (_) async => json(500, {'code': 50001, 'http': 500, 'error': 'oops'}),
        ).revokeToken(base, token: 't', tokenId: 'id'),
        ApiFailure.server,
      );
      await expectFailure(
        apiFor((_) => throw http.ClientException('down'))
            .revokeToken(base, token: 't', tokenId: 'id'),
        ApiFailure.unreachable,
      );
    });
  });

  group('publish', () {
    const stored = {
      'id': '01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c21',
      'time': 1700000000,
      'event': 'message',
      'topic': 'alerts',
      'title': 'Hi',
      'message': 'hello there',
      'priority': 4,
    };

    test(
      'posts JSON to /v1/messages and returns the message as stored',
      () async {
        http.Request? seen;
        final api = apiFor((request) async {
          seen = request;
          return json(200, stored);
        });
        final sent = await api.publish(
          Uri.parse('https://example.com/notify'),
          topic: 'alerts',
          message: 'hello there',
          title: 'Hi',
          priority: 4,
        );

        expect(seen!.method, 'POST');
        expect(seen!.url.toString(), 'https://example.com/notify/v1/messages');
        expect(jsonDecode(seen!.body), {
          'topic': 'alerts',
          'message': 'hello there',
          'title': 'Hi',
          'priority': 4,
        });
        expect(seen!.headers.containsKey('Authorization'), isFalse);
        expect(
          (sent.id, sent.topic, sent.title, sent.body, sent.priority),
          (
            '01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c21',
            'alerts',
            'Hi',
            'hello there',
            4,
          ),
        );
      },
    );

    test('leaves out an empty title and the default priority', () async {
      http.Request? seen;
      await apiFor((request) async {
        seen = request;
        return json(200, stored);
      }).publish(base, topic: 'alerts', message: 'x', title: '');
      expect(jsonDecode(seen!.body), {'topic': 'alerts', 'message': 'x'});
    });

    test('signs the request when signed in', () async {
      http.Request? seen;
      await apiFor((request) async {
        seen = request;
        return json(200, stored);
      }).publish(base, topic: 'alerts', message: 'x', token: 'flr_secret');
      expect(seen!.headers['Authorization'], 'Bearer flr_secret');
    });

    test('says what went wrong', () async {
      Future<void> check(http.Response response, ApiFailure failure) =>
          expectFailure(
            apiFor((_) async => response)
                .publish(base, topic: 'alerts', message: 'x'),
            failure,
          );

      await check(
        json(401, {'code': 40101, 'http': 401, 'error': 'log in first'}),
        ApiFailure.needsSignIn,
      );
      await check(
        json(401, {'code': 40102, 'http': 401, 'error': 'bad token'}),
        ApiFailure.badCredentials,
      );
      await check(
        json(403, {'code': 40301, 'http': 403, 'error': 'no write access'}),
        ApiFailure.forbidden,
      );
      await check(
        json(413, {'code': 41301, 'http': 413, 'error': 'too large'}),
        ApiFailure.tooLarge,
      );
      await check(
        json(429, {'code': 42904, 'http': 429, 'error': 'slow down'}),
        ApiFailure.throttled,
      );
      await check(
        json(500, {'code': 50001, 'http': 500, 'error': 'oops'}),
        ApiFailure.server,
      );
      await check(http.Response('<html>', 502), ApiFailure.server);
      await check(json(200, {'ok': true}), ApiFailure.server);
    });

    test('reports an unreachable server', () async {
      await expectFailure(
        apiFor((_) => throw http.ClientException('no route'))
            .publish(base, topic: 't', message: 'x'),
        ApiFailure.unreachable,
      );
    });
  });
}
