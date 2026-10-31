import 'dart:async';
import 'dart:convert';

import 'package:app/core/db/database.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/features/servers/sign_in_form.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/providers.dart';
import 'package:app/net/server_status.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'auth/memory_store.dart';
import 'helpers.dart';
import 'net/spy_manager.dart';

final _user = find.widgetWithText(TextFormField, 'User name');
final _password = find.widgetWithText(TextFormField, 'Password');
final _submit = find.widgetWithText(FilledButton, 'Sign in');

http.Response _login(String user) => http.Response(
  jsonEncode({
    'id': 'tok_1',
    'created': 1,
    'token': 'flr_secret',
    'user': user,
    'role': 'user',
  }),
  200,
);

http.Response _error(int status, int code) => http.Response(
  jsonEncode({'code': code, 'http': status, 'error': 'no'}),
  status,
);

void main() {
  late MemorySecretStore secrets;
  late SpyManager spy;
  late List<http.Request> requests;
  late FutureOr<http.Response> Function(http.Request) answer;

  /// The API and manager the tests use: answers come from [answer].
  List<Override> overrides(AppDatabase db) {
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
    ];
  }

  setUp(() {
    secrets = MemorySecretStore();
    requests = [];
    answer = (_) => _login('alice');
  });

  Future<void> open(
    WidgetTester tester,
    AppDatabase db,
    String location, {
    double width = 390,
  }) => pumpApp(
    tester,
    db,
    width: width,
    location: location,
    secrets: secrets,
    overrides: overrides(db),
  );

  group('sign-in form', () {
    testApp('opens as a sheet on a phone and a dialog on a wide window', (
      tester,
      db,
    ) async {
      await open(tester, db, '/signin/1');
      expect(find.byType(BottomSheet), findsOneWidget);
      expect(find.text('Sign in to example.com'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());

      await open(tester, db, '/signin/1', width: 1400);
      expect(find.byType(Dialog), findsOneWidget);
      expect(find.byType(SignInForm), findsOneWidget);
    });

    testApp('asks for the user name and the password', (tester, db) async {
      await open(tester, db, '/signin/1');
      await tester.tap(_submit);
      await settle(tester);

      expect(find.text('Enter your user name'), findsOneWidget);
      expect(find.text('Enter your password'), findsOneWidget);
      expect(requests, isEmpty);
    });

    testApp('signs in, keeps the token, reconnects the server and closes', (
      tester,
      db,
    ) async {
      await open(tester, db, '/signin/1');
      await tester.enterText(_user, '  alice ');
      await tester.enterText(_password, 'hunter2');
      await tester.tap(_submit);
      await settle(tester);

      final basic = requests.single.headers['Authorization']!.substring(
        'Basic '.length,
      );
      expect(utf8.decode(base64.decode(basic)), 'alice:hunter2');
      expect(secrets.values.keys, ['credentials:https://example.com']);
      expect(secrets.values.values.any((v) => v.contains('hunter2')), isFalse);
      expect(find.byType(SignInForm), findsNothing);
      expect(spy.loginChanges, ['https://example.com']);
    });

    testApp('hides the password until asked', (tester, db) async {
      await open(tester, db, '/signin/1');
      EditableText field() => tester.widget<EditableText>(
        find.descendant(of: _password, matching: find.byType(EditableText)),
      );
      expect(field().obscureText, isTrue);

      await tester.tap(find.byTooltip('Show password'));
      await tester.pump();
      expect(field().obscureText, isFalse);
      expect(find.byTooltip('Hide password'), findsOneWidget);
    });

    testApp('says the password is wrong, and stays open', (tester, db) async {
      answer = (_) => _error(401, 40102);
      await open(tester, db, '/signin/1');
      await tester.enterText(_user, 'alice');
      await tester.enterText(_password, 'wrong');
      await tester.tap(_submit);
      await settle(tester);

      expect(
        find.text('That user name or password is not right.'),
        findsOneWidget,
      );
      expect(find.byType(SignInForm), findsOneWidget);
      expect(secrets.values, isEmpty);

      // The button works again, so the user can try once more.
      answer = (_) => _login('alice');
      await tester.enterText(_password, 'right');
      await tester.tap(_submit);
      await settle(tester);
      expect(find.byType(SignInForm), findsNothing);
    });

    testApp('says when there were too many attempts', (tester, db) async {
      answer = (_) => _error(429, 42902);
      await open(tester, db, '/signin/1');
      await tester.enterText(_user, 'alice');
      await tester.enterText(_password, 'x');
      await tester.tap(_submit);
      await settle(tester);

      expect(find.textContaining('Too many failed attempts'), findsOneWidget);
    });

    testApp('says when the server cannot be reached', (tester, db) async {
      answer = (_) => throw http.ClientException('down');
      await open(tester, db, '/signin/1');
      await tester.enterText(_user, 'alice');
      await tester.enterText(_password, 'x');
      await tester.tap(_submit);
      await settle(tester);

      expect(find.textContaining("Can't reach example.com"), findsOneWidget);
    });

    testApp('does not send the form twice while it is waiting', (
      tester,
      db,
    ) async {
      final gate = Completer<http.Response>();
      answer = (_) => gate.future;
      await open(tester, db, '/signin/1');
      await tester.enterText(_user, 'alice');
      await tester.enterText(_password, 'x');
      await tester.tap(_submit);
      await tester.pump();

      expect(tester.widget<FilledButton>(_submit).onPressed, isNull);
      await tester.tap(_submit, warnIfMissed: false);
      expect(requests, hasLength(1));

      gate.complete(_login('alice'));
      await settle(tester);
    });

    testApp('warns before sending a password over plain http', (
      tester,
      db,
    ) async {
      await db
          .into(db.servers)
          .insert(
            ServersCompanion.insert(
              host: '192.168.1.5:8080',
              url: 'http://192.168.1.5:8080',
            ),
          );
      await open(tester, db, '/signin/2');

      expect(find.textContaining('plain http'), findsOneWidget);
      expect(
        find.widgetWithText(FilledButton, 'Sign in anyway'),
        findsOneWidget,
      );
      expect(find.widgetWithText(FilledButton, 'Sign in'), findsNothing);
    });

    testApp('does not warn about https', (tester, db) async {
      await open(tester, db, '/signin/1');
      expect(find.textContaining('plain http'), findsNothing);
      expect(find.widgetWithText(FilledButton, 'Sign in anyway'), findsNothing);
    });

    testApp('a link straight to it closes to Topics', (tester, db) async {
      await open(tester, db, '/signin/1');
      await tester.enterText(_user, 'alice');
      await tester.enterText(_password, 'x');
      await tester.tap(_submit);
      await settle(tester);

      expect(find.text('Topics'), findsWidgets);
    });

    testApp('meets the accessibility guidelines', (tester, db) async {
      final handle = tester.ensureSemantics();
      await open(tester, db, '/signin/1');
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await expectLater(tester, meetsGuideline(textContrastGuideline));
      handle.dispose();
    });
  });

  group('banners offer to sign in', () {
    testApp('when the server refused the saved sign-in', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        secrets: secrets,
        overrides: [
          ...overrides(db),
          connectionStatusProvider.overrideWith(
            (ref) => Stream.value({
              1: const ServerStatus(state: LinkState.authFailed),
            }),
          ),
        ],
      );
      expect(find.text('Try now'), findsNothing);
      await tester.tap(find.widgetWithText(TextButton, 'Sign in'));
      await settle(tester);

      expect(find.text('Sign in to example.com'), findsOneWidget);
    });

    testApp(
      'when a topic needs a sign-in, but not when access is simply forbidden',
      (tester, db) async {
        Future<void> showWith(int code) => pumpApp(
          tester,
          db,
          width: 390,
          secrets: secrets,
          location: '/topic/example.com/alerts-db',
          overrides: [
            ...overrides(db),
            connectionStatusProvider.overrideWith(
              (ref) => Stream.value({
                1: ServerStatus(
                  state: LinkState.connected,
                  topics: {'alerts-db': TopicLink(errorCode: code)},
                ),
              }),
            ),
          ],
        );

        await showWith(40301);
        expect(find.widgetWithText(TextButton, 'Sign in'), findsNothing);
        await tester.pumpWidget(const SizedBox.shrink());

        await showWith(40101);
        await tester.tap(find.widgetWithText(TextButton, 'Sign in'));
        await settle(tester);
        expect(find.text('Sign in to example.com'), findsOneWidget);
      },
    );
  });
}
