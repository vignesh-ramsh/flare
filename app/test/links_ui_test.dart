import 'package:app/core/db/database.dart';
import 'package:app/brand/brand.g.dart';
import 'package:app/data/messages_repository.dart';
import 'package:app/ui/link_opener.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:flutter_test/flutter_test.dart';

import 'data/test_db.dart';
import 'helpers.dart';

class FakeOpener implements LinkOpener {
  final List<Uri> opened = [];
  bool works = true;

  @override
  Future<bool> open(Uri uri) async {
    opened.add(uri);
    return works;
  }
}

void main() {
  group('which addresses the app opens', () {
    test('web addresses, in any case of scheme', () {
      expect(
        webLink('https://example.com/a?b=1#c'),
        Uri.parse('https://example.com/a?b=1#c'),
      );
      expect(webLink('HTTP://example.com'), isNotNull);
      expect(
        webLink('  https://example.com  '),
        Uri.parse('https://example.com'),
      );
      expect(webLink('http://10.0.0.1:8080/x'), isNotNull);
    });

    test('nothing a stranger could use to reach into the device', () {
      for (final text in [
        'file:///etc/passwd',
        'javascript:alert(1)',
        'intent://scan/#Intent;scheme=zxing;end',
        'tel:+123456',
        'sms:+123456',
        'mailto:someone@example.com',
        'market://details?id=x',
        'content://contacts/people',
        'flare://subscribe/example.com/alerts',
        'ftp://example.com/file',
        'data:text/html,hi',
      ]) {
        expect(webLink(text), isNull, reason: text);
      }
    });

    test('nothing that is not an address', () {
      for (final text in [
        null,
        '',
        '   ',
        'example.com',
        'https://',
        'https:///path',
        'not a link',
      ]) {
        expect(webLink(text), isNull, reason: '$text');
      }
    });
  });

  group('a link in a message', () {
    late FakeOpener opener;
    List<Override> overrides() => [
      linkOpenerProvider.overrideWithValue(opener),
    ];

    setUp(() => opener = FakeOpener());

    Future<void> withMessage(
      WidgetTester tester,
      AppDatabase db,
      String? click,
    ) async {
      await tester.runAsync(() async {
        await MessagesRepository(db).store(
          incoming(
            1,
            'quiet',
            90,
            title: 'Runbook',
            body: 'See the page',
            click: click,
          ),
        );
      });
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/quiet',
        overrides: overrides(),
      );
    }

    testApp('opens in the browser when tapped', (tester, db) async {
      await withMessage(tester, db, 'https://example.com/runbook');
      await tester.tap(find.text('https://example.com/runbook'));
      await tester.pump();
      expect(opener.opened, [Uri.parse('https://example.com/runbook')]);
      expect(find.text('Could not open that link.'), findsNothing);
    });

    testApp('says so when nothing could open it', (tester, db) async {
      opener.works = false;
      await withMessage(tester, db, 'https://example.com/runbook');
      await tester.tap(find.text('https://example.com/runbook'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));
      expect(find.text('Could not open that link.'), findsOneWidget);
    });

    testApp('is a link to a screen reader, and big enough to tap', (
      tester,
      db,
    ) async {
      final handle = tester.ensureSemantics();
      await withMessage(tester, db, 'https://example.com/runbook');
      final semantics = tester.getSemantics(
        find.text('https://example.com/runbook'),
      );
      expect(semantics.getSemanticsData().flagsCollection.isLink, isTrue);
      expect(
        tester
            .getSize(
              find
                  .ancestor(
                    of: find.text('https://example.com/runbook'),
                    matching: find.byType(InkWell),
                  )
                  .first,
            )
            .height,
        greaterThanOrEqualTo(48),
      );
      handle.dispose();
    });

    for (final unsafe in [
      'javascript:alert(1)',
      'file:///etc/passwd',
      'intent://x#Intent;end',
      'tel:+123',
    ]) {
      testApp('is only shown, never opened, when it is $unsafe', (
        tester,
        db,
      ) async {
        await withMessage(tester, db, unsafe);
        expect(find.text(unsafe), findsOneWidget);
        await tester.tap(find.text(unsafe));
        await tester.pump();
        expect(opener.opened, isEmpty);
        expect(
          find.descendant(
            of: find.byType(Card),
            matching: find.byType(InkWell),
          ),
          findsNothing,
        );
      });
    }

    testApp('is absent when the message has none', (tester, db) async {
      await withMessage(tester, db, null);
      expect(
        find.descendant(of: find.byType(Card), matching: find.byType(InkWell)),
        findsNothing,
      );
    });
  });

  group('About', () {
    testApp('opens the website the brand names', (tester, db) async {
      final opener = FakeOpener();
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/settings/about',
        overrides: [linkOpenerProvider.overrideWithValue(opener)],
      );

      expect(Brand.websiteUrl, isNotEmpty);
      await tester.tap(find.widgetWithText(ListTile, 'Website'));
      await tester.pump();
      expect(opener.opened, [Uri.parse(Brand.websiteUrl)]);
    });
  });
}
