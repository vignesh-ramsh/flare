import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:app/brand/brand.g.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/providers.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/subscribe_link.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/features/add/add_screen.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/providers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'helpers.dart';

final _server = find.widgetWithText(TextFormField, 'Server');
final _topic = find.widgetWithText(TextFormField, 'Topic');
final _submit = find.widgetWithText(FilledButton, 'Subscribe');

String _text(WidgetTester tester, Finder field) =>
    tester.widget<TextFormField>(field).controller!.text;

http.Response _config(String access) => http.Response(
  jsonEncode({
    'api_version': 1,
    'name': 'Test server',
    'auth': {'default_access': access},
    'features': ['websocket'],
  }),
  200,
);

void main() {
  late List<Uri> asked;
  late FutureOr<http.Response> Function(Uri) answer;

  setUp(() {
    asked = [];
    answer = (_) => _config('read-write');
  });

  List<Override> overrides({Random? random}) => [
    apiProvider.overrideWithValue(
      ServerApi(
        client: MockClient((request) async {
          asked.add(request.url);
          return answer(request.url);
        }),
      ),
    ),
    if (random != null) randomProvider.overrideWithValue(random),
  ];

  Future<void> open(
    WidgetTester tester,
    AppDatabase db, {
    String location = '/add',
    double width = 390,
    Random? random,
    List<Override> more = const [],
  }) => pumpApp(
    tester,
    db,
    width: width,
    location: location,
    overrides: [
      ...overrides(random: random),
      ...more,
    ],
  );

  /// Lets the server lookup's delay pass, and its answer arrive.
  Future<void> afterLookup(WidgetTester tester) async {
    await tester.pump(const Duration(milliseconds: 700));
    await settle(tester);
  }

  group('known servers', () {
    testApp('are offered in a menu, and choosing one fills the field', (
      tester,
      db,
    ) async {
      await tester.runAsync(
        () =>
            TopicsRepository(db)
                .subscribe(ServerAddress.parse('http://10.0.0.2:8080')!, 'lan'),
      );
      await open(tester, db);

      await tester.tap(find.byTooltip('Choose a server you already use'));
      await settle(tester);
      expect(find.text('https://example.com'), findsWidgets);
      expect(find.text('http://10.0.0.2:8080'), findsOneWidget);

      await tester.tap(find.text('http://10.0.0.2:8080'));
      await settle(tester);
      expect(_text(tester, _server), 'http://10.0.0.2:8080');
    });
  });

  group('with no server known yet', () {
    testApp('has no menu to choose from', (tester, db) async {
      await open(tester, db);
      expect(find.byTooltip('Choose a server you already use'), findsNothing);
    }, seeded: false);
  });

  group('a made-up name', () {
    testApp('fills the topic with 16 letters and digits', (tester, db) async {
      await open(tester, db, random: Random(7));
      await tester.tap(find.byTooltip('Make up a name nobody will guess'));
      await settle(tester);

      expect(_text(tester, _topic), randomTopicName(Random(7)));
      expect(_text(tester, _topic), matches(RegExp(r'^[a-z0-9]{16}$')));
    });

    testApp('differs each time, and replaces what was typed', (
      tester,
      db,
    ) async {
      await open(tester, db);
      await tester.enterText(_topic, 'typed');
      final seen = <String>{};
      for (var i = 0; i < 5; i++) {
        await tester.tap(find.byTooltip('Make up a name nobody will guess'));
        await settle(tester);
        seen.add(_text(tester, _topic));
      }
      expect(seen, hasLength(5));
      expect(seen, isNot(contains('typed')));
    });

    testApp('can be subscribed to straight away', (tester, db) async {
      await open(tester, db, random: Random(7));
      await tester.tap(find.byTooltip('Make up a name nobody will guess'));
      await settle(tester);
      await tester.tap(_submit);
      await settle(tester);

      final names = (await tester.runAsync(() => db.select(db.topics).get()))!
          .map((t) => t.name);
      expect(names, contains(randomTopicName(Random(7))));
    });

    testApp('clears a "you are already subscribed" error', (tester, db) async {
      await open(tester, db);
      await tester.enterText(_topic, 'backups');
      await tester.tap(_submit);
      await settle(tester);
      expect(
        find.text('You are already subscribed to this topic'),
        findsOneWidget,
      );

      await tester.tap(find.byTooltip('Make up a name nobody will guess'));
      await settle(tester);
      expect(
        find.text('You are already subscribed to this topic'),
        findsNothing,
      );
    });
  });

  group('pasting a link', () {
    testApp('into the topic fills the server and the topic', (
      tester,
      db,
    ) async {
      await open(tester, db);
      await tester.enterText(
        _topic,
        '${Brand.urlScheme}://subscribe/push.example.org:8443/ops',
      );
      await settle(tester);

      expect(_text(tester, _server), 'https://push.example.org:8443');
      expect(_text(tester, _topic), 'ops');
    });

    testApp('of a server that only speaks http keeps http', (tester, db) async {
      await open(tester, db);
      await tester.enterText(
        _topic,
        '${Brand.urlScheme}://subscribe/10.0.0.2:8080/lan?scheme=http',
      );
      await settle(tester);

      expect(_text(tester, _server), 'http://10.0.0.2:8080');
      expect(_text(tester, _topic), 'lan');
    });

    testApp('a topic\'s web address works too', (tester, db) async {
      await open(tester, db);
      await tester.enterText(_topic, 'https://ntfy.example.net/alerts');
      await settle(tester);

      expect(_text(tester, _server), 'https://ntfy.example.net');
      expect(_text(tester, _topic), 'alerts');
    });

    testApp('into the server field fills the topic as well', (
      tester,
      db,
    ) async {
      await open(tester, db);
      await tester.enterText(
        _server,
        '${Brand.urlScheme}://subscribe/push.example.org/ops',
      );
      await settle(tester);

      expect(_text(tester, _server), 'https://push.example.org');
      expect(_text(tester, _topic), 'ops');
    });

    testApp('then subscribes to what it said', (tester, db) async {
      await open(tester, db);
      await tester.enterText(
        _topic,
        '${Brand.urlScheme}://subscribe/push.example.org/ops',
      );
      await settle(tester);
      await tester.tap(_submit);
      await settle(tester);

      final rows = (await tester.runAsync(() => db.select(db.servers).get()))!;
      expect(rows.map((s) => s.url), contains('https://push.example.org'));
      final topics = (await tester.runAsync(() => db.select(db.topics).get()))!;
      expect(topics.map((t) => t.name), contains('ops'));
    });

    testApp('something that is not a link is left as typed', (
      tester,
      db,
    ) async {
      await open(tester, db);
      await tester.enterText(_topic, 'https://example.com/a/b');
      await settle(tester);

      expect(_text(tester, _topic), 'https://example.com/a/b');
      expect(_text(tester, _server), 'https://example.com');
    });
  });

  group('what the server says', () {
    testApp('an open server: the name is the secret', (tester, db) async {
      await open(tester, db);
      await tester.enterText(_server, 'https://open.example.org');
      await afterLookup(tester);

      expect(asked, [Uri.parse('https://open.example.org/v1/config')]);
      expect(
        find.textContaining(
          'open.example.org is open: anyone who knows a topic',
        ),
        findsOneWidget,
      );
    });

    testApp('a server that keeps topics private says to sign in', (
      tester,
      db,
    ) async {
      answer = (_) => _config('deny-all');
      await open(tester, db);
      await tester.enterText(_server, 'https://closed.example.org');
      await afterLookup(tester);

      expect(
        find.textContaining('closed.example.org keeps its topics private'),
        findsOneWidget,
      );
    });

    testApp('a server that is read-only says sending needs a sign-in', (
      tester,
      db,
    ) async {
      answer = (_) => _config('read-only');
      await open(tester, db);
      await tester.enterText(_server, 'https://ro.example.org');
      await afterLookup(tester);

      expect(find.textContaining('Sending needs a sign-in.'), findsOneWidget);
    });

    testApp(
      'a server that does not answer: says so, and that subscribing still works',
      (tester, db) async {
        answer = (_) => throw http.ClientException('down');
        await open(tester, db);
        await tester.enterText(_server, 'https://down.example.org');
        await afterLookup(tester);

        expect(
          find.textContaining("Can't reach down.example.org"),
          findsOneWidget,
        );
        expect(find.textContaining('You can still subscribe'), findsOneWidget);

        await tester.enterText(_topic, 'later');
        await tester.tap(_submit);
        await settle(tester);
        final names = (await tester.runAsync(() => db.select(db.topics).get()))!
            .map((t) => t.name);
        expect(names, contains('later'));
      },
    );

    testApp('an address that is not a server of this kind', (tester, db) async {
      answer = (_) => http.Response('<html></html>', 200);
      await open(tester, db);
      await tester.enterText(_server, 'https://web.example.org');
      await afterLookup(tester);

      expect(
        find.textContaining('does not look like a server this app can use'),
        findsOneWidget,
      );
    });

    testApp('waits until the user stops typing, and asks once', (
      tester,
      db,
    ) async {
      await open(tester, db);
      for (final text in [
        'h',
        'ht',
        'https://a',
        'https://a.example',
        'https://a.example.org',
      ]) {
        await tester.enterText(_server, text);
        await tester.pump(const Duration(milliseconds: 200));
      }
      expect(asked, isEmpty);
      await afterLookup(tester);
      expect(asked, [Uri.parse('https://a.example.org/v1/config')]);
    });

    testApp('does not ask about an address that is not one', (
      tester,
      db,
    ) async {
      await open(tester, db);
      await tester.enterText(_server, 'not a server');
      await afterLookup(tester);
      expect(asked, isEmpty);
    });

    testApp('forgets its note as soon as the address changes', (
      tester,
      db,
    ) async {
      await open(tester, db);
      await tester.enterText(_server, 'https://open.example.org');
      await afterLookup(tester);
      expect(find.textContaining('is open'), findsOneWidget);

      await tester.enterText(_server, 'https://open.example.or');
      await tester.pump();
      expect(find.textContaining('is open'), findsNothing);
    });

    testApp(
      'ignores a slow answer about an address the user has moved on from',
      (tester, db) async {
        final slow = Completer<http.Response>();
        answer = (uri) =>
            uri.host == 'slow.example.org' ? slow.future : _config('deny-all');
        await open(tester, db);

        await tester.enterText(_server, 'https://slow.example.org');
        await tester.pump(const Duration(milliseconds: 700));
        expect(asked, hasLength(1));

        await tester.enterText(_server, 'https://fast.example.org');
        await afterLookup(tester);
        expect(
          find.textContaining('fast.example.org keeps its topics private'),
          findsOneWidget,
        );

        slow.complete(_config('read-write'));
        await settle(tester);
        expect(find.textContaining('slow.example.org is open'), findsNothing);
        expect(
          find.textContaining('fast.example.org keeps its topics private'),
          findsOneWidget,
        );
      },
    );

    testApp('looks up the server it starts with', (tester, db) async {
      await open(tester, db);
      await afterLookup(tester);
      expect(asked, [Uri.parse('https://example.com/v1/config')]);
      expect(find.textContaining('example.com is open'), findsOneWidget);
    });

    testApp('looks up the server a link names', (tester, db) async {
      await open(tester, db, location: '/subscribe/push.example.org/ops');
      await afterLookup(tester);
      expect(asked, [Uri.parse('https://push.example.org/v1/config')]);
    });

    testApp('the note is announced to a screen reader', (tester, db) async {
      final handle = tester.ensureSemantics();
      await open(tester, db);
      await tester.enterText(_server, 'https://open.example.org');
      await afterLookup(tester);
      await expectLater(tester, meetsGuideline(textContrastGuideline));
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
      handle.dispose();
    });
  });

  group('opened by a link', () {
    testApp('fills in the server and the topic, for the user to confirm', (
      tester,
      db,
    ) async {
      await open(
        tester,
        db,
        location: '/subscribe/push.example.org%3A8443/ops',
      );
      expect(find.byType(AddScreen), findsOneWidget);
      expect(_text(tester, _server), 'https://push.example.org:8443');
      expect(_text(tester, _topic), 'ops');

      // Nothing is subscribed to until the button is pressed.
      expect(
        (await tester.runAsync(() => db.select(db.topics).get()))!
            .map((t) => t.name),
        isNot(contains('ops')),
      );
      await tester.tap(_submit);
      await settle(tester);
      expect(
        (await tester.runAsync(() => db.select(db.topics).get()))!
            .map((t) => t.name),
        contains('ops'),
      );
    });

    testApp('keeps plain http when the link says so', (tester, db) async {
      await open(
        tester,
        db,
        location: '/subscribe/10.0.0.2%3A8080/lan?scheme=http',
      );
      expect(_text(tester, _server), 'http://10.0.0.2:8080');
      expect(_text(tester, _topic), 'lan');
    });

    testApp('opens the plain form for a link that is not valid', (
      tester,
      db,
    ) async {
      await open(tester, db, location: '/subscribe/example.com/not%20valid');
      expect(find.byType(AddScreen), findsOneWidget);
      expect(_text(tester, _topic), isEmpty);
      expect(_text(tester, _server), 'https://example.com');
    });

    testApp('is the same as /add with a server and a topic', (
      tester,
      db,
    ) async {
      await open(
        tester,
        db,
        location: '/add?server=http%3A%2F%2F10.0.0.2%3A8080&topic=lan',
      );
      expect(_text(tester, _server), 'http://10.0.0.2:8080');
      expect(_text(tester, _topic), 'lan');
    });

    testApp('works in a dialog on a wide window too', (tester, db) async {
      await open(
        tester,
        db,
        width: 1400,
        location: '/subscribe/push.example.org/ops',
      );
      expect(find.byType(Dialog), findsOneWidget);
      expect(_text(tester, _topic), 'ops');
    });

    testApp('closing it, with nothing under it, goes to the topics', (
      tester,
      db,
    ) async {
      await open(tester, db, location: '/subscribe/push.example.org/ops');
      await tester.tap(find.text('Cancel'));
      await settle(tester);
      expect(find.byType(AddScreen), findsNothing);
      expect(find.text('alerts-db'), findsWidgets);
    });

    group('in a build locked to one server', () {
      final locked = [
        serverPolicyProvider.overrideWithValue(
          const ServerPolicy(
            defaultUrl: 'https://push.example.org',
            allowCustom: false,
          ),
        ),
      ];

      testApp('takes a topic on that server', (tester, db) async {
        await open(
          tester,
          db,
          location: '/subscribe/push.example.org/ops',
          more: locked,
        );
        expect(_server, findsNothing);
        expect(_text(tester, _topic), 'ops');
        expect(find.textContaining('only works with'), findsNothing);
      });

      testApp('refuses a topic on another server, and does not fill it in', (
        tester,
        db,
      ) async {
        await open(
          tester,
          db,
          location: '/subscribe/other.example.net/ops',
          more: locked,
        );
        expect(
          find.text(
            'This app only works with push.example.org, so it cannot subscribe to a topic on another server.',
          ),
          findsOneWidget,
        );
        expect(_text(tester, _topic), isEmpty);
      });

      testApp('ignores a pasted link to another server', (tester, db) async {
        await open(tester, db, more: locked);
        await tester.enterText(
          _topic,
          '${Brand.urlScheme}://subscribe/other.example.net/ops',
        );
        await settle(tester);
        expect(
          _text(tester, _topic),
          '${Brand.urlScheme}://subscribe/other.example.net/ops',
        );
      });
    });
  });
}
