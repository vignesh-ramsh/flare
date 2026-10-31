import 'package:app/app/app.dart';
import 'package:app/app/deep_link.dart';
import 'package:app/brand/brand.g.dart';
import 'package:app/data/providers.dart';
import 'package:app/features/add/add_screen.dart';
import 'package:app/features/welcome/welcome_pages.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'helpers.dart';

/// Sends the app a link the way the operating system does: as a "push route" message on the
/// platform's navigation channel, with the whole link as the location.
Future<void> deliver(WidgetTester tester, String link) async {
  const codec = JSONMethodCodec();
  await tester.binding.defaultBinaryMessenger.handlePlatformMessage(
    'flutter/navigation',
    codec.encodeMethodCall(
      MethodCall('pushRouteInformation', {'location': link}),
    ),
    (_) {},
  );
  await settle(tester);
}

final _server = find.widgetWithText(TextFormField, 'Server');
final _topic = find.widgetWithText(TextFormField, 'Topic');

String _text(WidgetTester tester, Finder field) =>
    tester.widget<TextFormField>(field).controller!.text;

void main() {
  const scheme = Brand.urlScheme;

  group('which link opens what', () {
    test('a subscribe link opens the form for it', () {
      expect(
        routeForLink('$scheme://subscribe/example.com/alerts'),
        '/subscribe/example.com/alerts',
      );
      expect(
        routeForLink('$scheme://subscribe/example.com:8443/alerts'),
        '/subscribe/example.com%3A8443/alerts',
      );
      expect(
        routeForLink('$scheme://subscribe/10.0.0.2:8080/lan?scheme=http'),
        '/subscribe/10.0.0.2%3A8080/lan?scheme=http',
      );
    });

    test('anything else is not the app\'s to open', () {
      for (final text in [
        '',
        'https://example.com/alerts',
        'http://example.com/alerts',
        '$scheme://settings',
        '$scheme://subscribe/example.com',
        '$scheme://subscribe/example.com/not valid',
        'other://subscribe/example.com/alerts',
        '--flag',
        '/topic/example.com/alerts',
      ]) {
        expect(routeForLink(text), isNull, reason: text);
      }
    });

    test('the scheme is whatever the brand says', () {
      expect(
        routeForLink('acme://subscribe/example.com/a', scheme: 'acme'),
        '/subscribe/example.com/a',
      );
      expect(
        routeForLink('$scheme://subscribe/example.com/a', scheme: 'acme'),
        isNull,
      );
    });

    test('the first link among a program\'s arguments is the one used', () {
      expect(
        routeFromArguments([
          '--verbose',
          '$scheme://subscribe/example.com/a',
          '$scheme://subscribe/example.com/b',
        ]),
        '/subscribe/example.com/a',
      );
      expect(routeFromArguments(['--verbose', 'file.txt']), isNull);
      expect(routeFromArguments([]), isNull);
    });
  });

  group('delivered by the operating system', () {
    testApp('opens the Subscribe form, filled in, over the topics', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390);
      await deliver(tester, '$scheme://subscribe/push.example.org/ops');

      expect(find.byType(AddScreen), findsOneWidget);
      expect(_text(tester, _server), 'https://push.example.org');
      expect(_text(tester, _topic), 'ops');
    });

    testApp('keeps a port and plain http', (tester, db) async {
      await pumpApp(tester, db, width: 390);
      await deliver(
        tester,
        '$scheme://subscribe/10.0.0.2:8080/lan?scheme=http',
      );

      expect(_text(tester, _server), 'http://10.0.0.2:8080');
      expect(_text(tester, _topic), 'lan');
    });

    testApp('subscribes only when the user presses the button', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390);
      await deliver(tester, '$scheme://subscribe/push.example.org/ops');
      final before = (await tester.runAsync(() => db.select(db.topics).get()))!;
      expect(before.map((t) => t.name), isNot(contains('ops')));

      await tester.tap(find.widgetWithText(FilledButton, 'Subscribe'));
      await settle(tester);
      final after = (await tester.runAsync(() => db.select(db.topics).get()))!;
      expect(after.map((t) => t.name), contains('ops'));
    });

    testApp('works as a dialog in a wide window', (tester, db) async {
      await pumpApp(tester, db, width: 1400);
      await deliver(tester, '$scheme://subscribe/push.example.org/ops');
      expect(find.byType(Dialog), findsOneWidget);
      expect(_text(tester, _topic), 'ops');
    });

    testApp('a link that is not a subscribe link opens the topics', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390);
      await deliver(tester, '$scheme://nonsense/here');
      expect(find.byType(AddScreen), findsNothing);
      expect(find.text('alerts-db'), findsWidgets);
    });

    testApp(
      'a subscribe link for a topic that cannot exist opens the plain form',
      (tester, db) async {
        await pumpApp(tester, db, width: 390);
        await deliver(tester, '$scheme://subscribe/example.com/has%20space');
        // Not a subscribe link at all, so not the form: the topics.
        expect(find.byType(AddScreen), findsNothing);
      },
    );

    testApp('another link while the form is open replaces what it shows', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390);
      await deliver(tester, '$scheme://subscribe/push.example.org/first');
      await deliver(tester, '$scheme://subscribe/push.example.org/second');
      expect(find.byType(AddScreen), findsOneWidget);
      expect(_text(tester, _topic), 'second');
    });
  });

  group('on the first run', () {
    testApp('a link skips the welcome pages, since it names the server', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, useAppRouter: true);
      await deliver(tester, '$scheme://subscribe/push.example.org/ops');

      expect(find.byType(WelcomePage), findsNothing);
      expect(find.byType(AddScreen), findsOneWidget);
      expect(_text(tester, _topic), 'ops');
    }, seeded: false);

    testApp('subscribing from it ends the first run and opens the topic', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, useAppRouter: true);
      await deliver(tester, '$scheme://subscribe/push.example.org/ops');
      await tester.tap(find.widgetWithText(FilledButton, 'Subscribe'));
      await settle(tester);

      expect(find.byType(AddScreen), findsNothing);
      expect(find.byType(WelcomePage), findsNothing);
      expect(find.text('ops'), findsWidgets);
    }, seeded: false);

    testApp('without a link, everything still goes to the welcome pages', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, useAppRouter: true);
      expect(find.byType(WelcomePage), findsOneWidget);
    }, seeded: false);
  });

  group('given on the command line', () {
    testApp('opens the form when the program starts with a link', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        useAppRouter: true,
        overrides: [
          initialLocationProvider.overrideWithValue(
            routeFromArguments(['$scheme://subscribe/push.example.org/ops'])!,
          ),
        ],
        location: '/',
      );
      // The app's own router starts where the override said, before any test navigation.
      expect(find.byType(AddScreen), findsOneWidget);
      expect(_text(tester, _topic), 'ops');
    });
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

    testApp('a link to its server fills in the topic', (tester, db) async {
      await pumpApp(tester, db, width: 390, overrides: locked);
      await deliver(tester, '$scheme://subscribe/push.example.org/ops');
      expect(_text(tester, _topic), 'ops');
    });

    testApp('a link to another server is refused, in words', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, overrides: locked);
      await deliver(tester, '$scheme://subscribe/other.example.net/ops');
      expect(
        find.textContaining('only works with push.example.org'),
        findsOneWidget,
      );
      expect(_text(tester, _topic), isEmpty);
    });
  });
}
