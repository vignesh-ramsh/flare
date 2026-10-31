import 'dart:async';
import 'dart:convert';

import 'package:app/auth/credentials.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/providers.dart';
import 'package:app/data/servers_repository.dart';
import 'package:app/data/startup.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/features/welcome/welcome_pages.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/providers.dart';
import 'package:app/net/server_status.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'auth/memory_store.dart';
import 'data/test_db.dart';
import 'helpers.dart';
import 'net/spy_manager.dart';

const _config = {
  'api_version': 1,
  'name': 'Acme Alerts',
  'primary_color': '#F97316',
  'app_url_scheme': 'acme',
  'features': ['publish', 'websocket'],
  'limits': {'message_size': 4096, 'cache_duration': 86400},
  'auth': {'default_access': 'deny-all'},
};

http.Response _json(int status, Object body) =>
    http.Response(jsonEncode(body), status);

void main() {
  late MemorySecretStore secrets;
  late SpyManager spy;
  late List<http.Request> requests;
  late FutureOr<http.Response> Function(http.Request) answer;

  /// A server that is a Flare server, with an account `alice`.
  http.Response flare(http.Request request) {
    if (request.url.path.endsWith('/v1/config')) return _json(200, _config);
    if (request.url.path.endsWith('/v1/auth/login')) {
      return _json(200, {
        'id': 'tok_1',
        'created': 1,
        'token': 'flr_secret',
        'user': 'alice',
        'role': 'user',
      });
    }
    if (request.method == 'DELETE') return http.Response('', 204);
    return http.Response('', 404);
  }

  List<Override> overrides(AppDatabase db, [List<Override> more = const []]) {
    spy = SpyManager(TopicsRepository(db));
    return [
      apiProvider.overrideWithValue(
        ServerApi(
          client: MockClient((request) async {
            requests.add(request);
            return answer(request);
          }),
        ),
      ),
      connectionManagerProvider.overrideWithValue(spy),
      ...more,
    ];
  }

  setUp(() {
    secrets = MemorySecretStore();
    requests = [];
    answer = flare;
  });

  Future<void> open(
    WidgetTester tester,
    AppDatabase db,
    String location, {
    double width = 390,
    List<Override> more = const [],
    bool firstRun = false,
    SecretStore? store,
    Map<String, Credentials> signedIn = const {},
  }) => pumpApp(
    tester,
    db,
    width: width,
    location: location,
    secrets: store ?? secrets,
    signedIn: signedIn,
    useAppRouter: firstRun,
    overrides: overrides(db, more),
  );

  final address = find.widgetWithText(TextFormField, 'Server');

  group('first run', () {
    testApp('shows the welcome page until a server is chosen', (
      tester,
      db,
    ) async {
      await open(tester, db, '/', firstRun: true);

      expect(find.textContaining('Welcome to'), findsOneWidget);
      expect(find.text('Get started'), findsOneWidget);
      expect(find.byType(NavigationBar), findsNothing);
    }, seeded: false);

    testApp('sends every route there while there is no server', (
      tester,
      db,
    ) async {
      await open(tester, db, '/settings', firstRun: true);
      expect(find.byType(WelcomePage), findsOneWidget);
    }, seeded: false);

    testApp('does not show it once there is a server', (tester, db) async {
      await open(tester, db, '/', firstRun: true);
      expect(find.byType(WelcomePage), findsNothing);
      expect(find.byType(NavigationBar), findsOneWidget);
    });

    testApp('is a full screen on a phone and a centred card on a wide window', (
      tester,
      db,
    ) async {
      await open(tester, db, '/welcome');
      expect(
        find.descendant(
          of: find.byType(WelcomePage),
          matching: find.byType(Card),
        ),
        findsNothing,
      );
      await tester.pumpWidget(const SizedBox.shrink());

      await open(tester, db, '/welcome', width: 1400);
      expect(
        find.descendant(
          of: find.byType(WelcomePage),
          matching: find.byType(Card),
        ),
        findsOneWidget,
      );
    });

    testApp('walks through: choose a server, then skip signing in', (
      tester,
      db,
    ) async {
      await open(tester, db, '/', firstRun: true);

      await tester.tap(find.text('Get started'));
      await settle(tester);
      expect(find.text('Choose a server'), findsOneWidget);

      await tester.enterText(address, 'example.org');
      await tester.tap(find.text('Check server'));
      await settle(tester);
      expect(find.text('Found Acme Alerts'), findsOneWidget);
      expect(
        find.text('You need to sign in to read its topics.'),
        findsOneWidget,
      );
      expect(requests.single.url.toString(), 'https://example.org/v1/config');

      await tester.tap(find.text('Continue'));
      await settle(tester);
      expect(find.text('Sign in to example.org'), findsOneWidget);

      await tester.tap(find.text('Skip for now'));
      await settle(tester);
      expect(find.text('No topics yet'), findsOneWidget);
      final servers = await tester.runAsync(() => db.select(db.servers).get());
      expect(servers!.map((s) => s.url), ['https://example.org']);
    }, seeded: false);

    testApp('walks through: choose a server, then sign in', (tester, db) async {
      await open(tester, db, '/', firstRun: true);
      await tester.tap(find.text('Get started'));
      await settle(tester);
      await tester.enterText(address, 'https://example.org');
      await tester.tap(find.text('Check server'));
      await settle(tester);
      await tester.tap(find.text('Continue'));
      await settle(tester);

      await tester.enterText(
        find.widgetWithText(TextFormField, 'User name'),
        'alice',
      );
      await tester.enterText(
        find.widgetWithText(TextFormField, 'Password'),
        'pw',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Sign in'));
      await settle(tester);

      expect(find.text('No topics yet'), findsOneWidget);
      expect(secrets.values.keys, ['credentials:https://example.org']);
      expect(spy.loginChanges, ['https://example.org']);
    }, seeded: false);

    testApp('says an open server can be used without signing in', (
      tester,
      db,
    ) async {
      answer = (request) => _json(200, {
        ..._config,
        'auth': {'default_access': 'read-write'},
      });
      await open(tester, db, '/welcome/server');
      await tester.enterText(address, 'example.org');
      await tester.tap(find.text('Check server'));
      await settle(tester);

      expect(find.text('You can use it without signing in.'), findsOneWidget);
    }, seeded: false);

    testApp(
      'says when the address cannot be reached, is not a server, or has a bad certificate',
      (tester, db) async {
        await open(tester, db, '/welcome/server');
        for (final (response, text)
            in <(FutureOr<http.Response> Function(http.Request), String)>[
              (
                (_) => throw http.ClientException('refused'),
                "Can't reach example.org. Check the address",
              ),
              (
                (_) => http.Response('<html>', 200),
                'does not look like a server this app can use',
              ),
              (
                (_) => throw http.ClientException('CERTIFICATE_VERIFY_FAILED'),
                'certificate was not accepted',
              ),
              (
                (_) => http.Response('', 500),
                'does not look like a server this app can use',
              ),
            ]) {
          answer = response;
          await tester.enterText(address, 'example.org');
          await tester.tap(find.text('Check server'));
          await settle(tester);
          expect(find.textContaining(text), findsOneWidget, reason: text);
        }
        final servers = await tester.runAsync(
          () => db.select(db.servers).get(),
        );
        expect(servers, isEmpty);
      },
      seeded: false,
    );

    testApp(
      'rejects an address that is not a web address without asking the network',
      (tester, db) async {
        await open(tester, db, '/welcome/server');
        await tester.enterText(address, 'ftp://example.org');
        await tester.tap(find.text('Check server'));
        await settle(tester);

        expect(
          find.textContaining('That is not a web address'),
          findsOneWidget,
        );
        expect(requests, isEmpty);
      },
      seeded: false,
    );

    testApp('forgets the result when the address is edited', (
      tester,
      db,
    ) async {
      await open(tester, db, '/welcome/server');
      await tester.enterText(address, 'example.org');
      await tester.tap(find.text('Check server'));
      await settle(tester);
      expect(find.text('Found Acme Alerts'), findsOneWidget);

      await tester.enterText(address, 'example.net');
      await tester.pump();
      expect(find.text('Found Acme Alerts'), findsNothing);
      expect(find.text('Check server'), findsOneWidget);
    }, seeded: false);

    testApp('goes back a step', (tester, db) async {
      await open(tester, db, '/welcome/server');
      await tester.tap(find.byType(BackButton));
      await settle(tester);
      expect(find.text('Get started'), findsOneWidget);
    }, seeded: false);

    testApp('a build locked to one server shows its address and checks that', (
      tester,
      db,
    ) async {
      await open(
        tester,
        db,
        '/welcome/server',
        more: [
          serverPolicyProvider.overrideWithValue(
            const ServerPolicy(
              defaultUrl: 'https://push.example.org',
              allowCustom: false,
            ),
          ),
        ],
      );
      expect(address, findsNothing);
      expect(find.text('https://push.example.org'), findsOneWidget);

      await tester.tap(find.text('Check server'));
      await settle(tester);
      expect(requests.single.url.host, 'push.example.org');
      expect(find.text('Found Acme Alerts'), findsOneWidget);
    }, seeded: false);

    testApp('removing the last server brings the welcome page back', (
      tester,
      db,
    ) async {
      final id = (await tester.runAsync(
        () => db
            .into(db.servers)
            .insert(
              ServersCompanion.insert(
                host: 'only.example',
                url: 'https://only.example',
              ),
            ),
      ))!;
      await open(tester, db, '/settings/servers/$id', firstRun: true);

      await tester.tap(find.text('Remove server'));
      await settle(tester);
      await tester.tap(find.widgetWithText(FilledButton, 'Remove'));
      await settle(tester);

      expect(find.byType(WelcomePage), findsOneWidget);
    }, seeded: false);

    testApp('meets the accessibility guidelines', (tester, db) async {
      final handle = tester.ensureSemantics();
      for (final location in ['/welcome', '/welcome/server']) {
        await open(tester, db, location);
        await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
        await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
        await expectLater(tester, meetsGuideline(textContrastGuideline));
        await tester.pumpWidget(const SizedBox.shrink());
      }
      handle.dispose();
    }, seeded: false);
  });

  group('locked builds', () {
    test('always have their server', () async {
      final db = memoryDb();
      addTearDown(db.close);
      final servers = ServersRepository(db);
      const locked = ServerPolicy(
        defaultUrl: 'https://push.example.org',
        allowCustom: false,
      );

      await ensureLockedServer(servers, locked);
      await ensureLockedServer(servers, locked);

      expect((await db.select(db.servers).get()).map((s) => s.url), [
        'https://push.example.org',
      ]);
    });

    test('and other builds add nothing', () async {
      final db = memoryDb();
      addTearDown(db.close);
      await ensureLockedServer(
        ServersRepository(db),
        const ServerPolicy(
          defaultUrl: 'https://push.example.org',
          allowCustom: true,
        ),
      );
      expect(await db.select(db.servers).get(), isEmpty);
    });

    test('a locked build with no address adds nothing', () async {
      final db = memoryDb();
      addTearDown(db.close);
      await ensureLockedServer(
        ServersRepository(db),
        const ServerPolicy(defaultUrl: '', allowCustom: false),
      );
      expect(await db.select(db.servers).get(), isEmpty);
    });
  });

  group('servers', () {
    const alice = Credentials(
      token: 'flr_secret',
      tokenId: 'tok_1',
      user: 'alice',
    );

    testApp('lists each with whether you are signed in', (tester, db) async {
      await db
          .into(db.servers)
          .insert(
            ServersCompanion.insert(
              host: 'other.example',
              url: 'https://other.example',
            ),
          );
      await secrets.write('credentials:https://example.com', alice.toJson());
      await open(
        tester,
        db,
        '/settings/servers',
        signedIn: {'https://example.com': alice},
      );

      expect(find.textContaining('Signed in as alice'), findsOneWidget);
      expect(find.textContaining('Not signed in'), findsOneWidget);
    }, seeded: true);

    testApp('shows one: its address, connection and account', (
      tester,
      db,
    ) async {
      await open(
        tester,
        db,
        '/settings/servers/1',
        more: [
          connectionStatusProvider.overrideWith(
            (ref) => Stream.value({
              1: const ServerStatus(state: LinkState.connected),
            }),
          ),
        ],
      );

      expect(find.text('https://example.com'), findsOneWidget);
      expect(find.text('Connected'), findsOneWidget);
      expect(find.text('Not signed in'), findsOneWidget);
    });

    testApp('says what each connection state means', (tester, db) async {
      for (final (state, text) in [
        (LinkState.connecting, 'Connecting'),
        (LinkState.waiting, 'Not connected'),
        (LinkState.authFailed, 'Sign-in not accepted'),
        (LinkState.idle, 'Not following any topic'),
      ]) {
        await open(
          tester,
          db,
          '/settings/servers/1',
          more: [
            connectionStatusProvider.overrideWith(
              (ref) => Stream.value({1: ServerStatus(state: state)}),
            ),
          ],
        );
        expect(find.text(text), findsOneWidget, reason: '$state');
        await tester.pumpWidget(const SizedBox.shrink());
      }
    });

    testApp('signs in from the detail page, and shows who you are', (
      tester,
      db,
    ) async {
      await open(tester, db, '/settings/servers/1');
      await tester.tap(find.widgetWithText(FilledButton, 'Sign in'));
      await settle(tester);
      await tester.enterText(
        find.widgetWithText(TextFormField, 'User name'),
        'alice',
      );
      await tester.enterText(
        find.widgetWithText(TextFormField, 'Password'),
        'pw',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Sign in').last);
      await settle(tester);

      expect(find.text('Signed in as alice'), findsOneWidget);
      expect(find.text('Sign out'), findsOneWidget);
    });

    testApp('signs out after asking, and revokes the token', (
      tester,
      db,
    ) async {
      await secrets.write('credentials:https://example.com', alice.toJson());
      await open(
        tester,
        db,
        '/settings/servers/1',
        signedIn: {'https://example.com': alice},
      );

      await tester.tap(find.widgetWithText(OutlinedButton, 'Sign out'));
      await settle(tester);
      expect(find.text('Sign out of example.com?'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, 'Sign out'));
      await settle(tester);

      expect(requests.single.method, 'DELETE');
      expect(requests.single.url.path, '/v1/account/tokens/tok_1');
      expect(secrets.values, isEmpty);
      expect(find.text('Not signed in'), findsOneWidget);
      expect(spy.loginChanges, ['https://example.com']);
    });

    testApp('says so when signing out could not reach the server', (
      tester,
      db,
    ) async {
      await secrets.write('credentials:https://example.com', alice.toJson());
      answer = (_) => throw http.ClientException('offline');
      await open(
        tester,
        db,
        '/settings/servers/1',
        signedIn: {'https://example.com': alice},
      );
      await tester.tap(find.widgetWithText(OutlinedButton, 'Sign out'));
      await settle(tester);
      await tester.tap(find.widgetWithText(FilledButton, 'Sign out'));
      await settle(tester);

      expect(find.textContaining('could not be reached'), findsOneWidget);
      expect(secrets.values, isEmpty);
    });

    testApp('does nothing if you cancel signing out', (tester, db) async {
      await secrets.write('credentials:https://example.com', alice.toJson());
      await open(
        tester,
        db,
        '/settings/servers/1',
        signedIn: {'https://example.com': alice},
      );
      await tester.tap(find.widgetWithText(OutlinedButton, 'Sign out'));
      await settle(tester);
      await tester.tap(find.text('Cancel'));
      await settle(tester);

      expect(requests, isEmpty);
      expect(secrets.values, isNotEmpty);
      expect(find.text('Signed in as alice'), findsOneWidget);
    });

    testApp('warns when the sign-in cannot be kept', (tester, db) async {
      await open(tester, db, '/settings/servers/1', store: BrokenSecretStore());
      await tester.tap(find.widgetWithText(FilledButton, 'Sign in'));
      await settle(tester);
      await tester.enterText(
        find.widgetWithText(TextFormField, 'User name'),
        'alice',
      );
      await tester.enterText(
        find.widgetWithText(TextFormField, 'Password'),
        'pw',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Sign in').last);
      await settle(tester);

      expect(find.text('Signed in as alice'), findsOneWidget);
      expect(find.textContaining('no secure place'), findsOneWidget);
    });

    testApp('removes a server with its topics and messages, after asking', (
      tester,
      db,
    ) async {
      await open(tester, db, '/settings/servers/1');
      await tester.tap(find.text('Remove server'));
      await settle(tester);
      expect(find.text('Remove example.com?'), findsOneWidget);

      await tester.tap(find.widgetWithText(FilledButton, 'Remove'));
      await settle(tester);

      expect(await tester.runAsync(() => db.select(db.servers).get()), isEmpty);
      expect(await tester.runAsync(() => db.select(db.topics).get()), isEmpty);
      expect(
        await tester.runAsync(() => db.select(db.messages).get()),
        isEmpty,
      );
    });

    testApp('keeps it if you cancel', (tester, db) async {
      await open(tester, db, '/settings/servers/1');
      await tester.tap(find.text('Remove server'));
      await settle(tester);
      await tester.tap(find.text('Cancel'));
      await settle(tester);

      expect(
        await tester.runAsync(() => db.select(db.servers).get()),
        hasLength(1),
      );
    });

    testApp('signs out of a server before removing it', (tester, db) async {
      await secrets.write('credentials:https://example.com', alice.toJson());
      await open(
        tester,
        db,
        '/settings/servers/1',
        signedIn: {'https://example.com': alice},
      );
      await tester.tap(find.text('Remove server'));
      await settle(tester);
      await tester.tap(find.widgetWithText(FilledButton, 'Remove'));
      await settle(tester);

      expect(requests.single.method, 'DELETE');
      expect(secrets.values, isEmpty);
    });

    testApp('adds a server from the list', (tester, db) async {
      await open(tester, db, '/settings/servers');
      await tester.tap(find.byTooltip('Add server'));
      await settle(tester);
      await tester.enterText(address, 'new.example');
      await tester.tap(find.text('Check server'));
      await settle(tester);
      await tester.tap(find.text('Continue'));
      await settle(tester);

      final servers = await tester.runAsync(() => db.select(db.servers).get());
      expect(servers!.map((s) => s.url), contains('https://new.example'));
      expect(
        find.text('https://new.example'),
        findsOneWidget,
      ); // its detail page
    });

    testApp('says so when the server is already there', (tester, db) async {
      await open(tester, db, '/settings/servers');
      await tester.tap(find.byTooltip('Add server'));
      await settle(tester);
      await tester.enterText(address, 'https://example.com');
      await tester.tap(find.text('Check server'));
      await settle(tester);
      await tester.tap(find.text('Continue'));
      await settle(tester);

      expect(find.text('You already added example.com.'), findsOneWidget);
      expect(
        await tester.runAsync(() => db.select(db.servers).get()),
        hasLength(1),
      );
    });

    testApp('a locked build cannot add or remove servers', (tester, db) async {
      final locked = serverPolicyProvider.overrideWithValue(
        const ServerPolicy(
          defaultUrl: 'https://example.com',
          allowCustom: false,
        ),
      );
      await open(tester, db, '/settings/servers', more: [locked]);
      expect(find.byTooltip('Add server'), findsNothing);
      await tester.pumpWidget(const SizedBox.shrink());

      await open(tester, db, '/settings/servers/1', more: [locked]);
      expect(find.text('Remove server'), findsNothing);
      expect(find.text('Sign in'), findsOneWidget);
    });

    testApp('says what is missing for an unknown server', (tester, db) async {
      await open(tester, db, '/settings/servers/99');
      expect(find.byType(Scaffold), findsWidgets);
      expect(find.text('Remove server'), findsNothing);
    });
  });
}
