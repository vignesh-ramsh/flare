import 'package:app/brand/brand.g.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'helpers.dart';

void main() {
  group('Settings', () {
    testApp('lists the servers with their topic counts', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/settings/servers');

      expect(find.text('example.com'), findsOneWidget);
      expect(find.textContaining('4 topics'), findsOneWidget);
    });

    testApp('says what to do when there are no servers', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/settings/servers');
      await tester.runAsync(() => db.delete(db.servers).go());
      await settle(tester);

      expect(find.textContaining('No servers yet'), findsOneWidget);
    });

    testApp('goes from Settings to Servers and back', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/settings');
      expect(find.text('1 server'), findsOneWidget);

      await tester.tap(find.text('Servers'));
      await settle(tester);
      expect(find.text('example.com'), findsOneWidget);
      expect(find.byType(NavigationBar), findsNothing);

      await tester.tap(find.byType(BackButton));
      await settle(tester);
      expect(find.text('1 server'), findsOneWidget);
      expect(find.byType(NavigationBar), findsOneWidget);
    });

    testApp('About names this build and what it is based on', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, location: '/settings/about');

      expect(find.text('About ${Brand.name}'), findsOneWidget);
      expect(find.text(Brand.name), findsOneWidget);
      expect(find.text(Brand.description), findsOneWidget);
      expect(find.text('Based on ${Brand.upstreamName}'), findsOneWidget);
      expect(find.text(Brand.upstreamUrl), findsWidgets);
    });

    testApp('About leaves out a link the brand does not have', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, location: '/settings/about');

      expect(
        Brand.privacyPolicyUrl,
        isEmpty,
        reason: 'this test assumes the sample brand',
      );
      expect(find.text('Privacy policy'), findsNothing);
      expect(find.text('Website'), findsOneWidget);
    });

    testApp('works in a wide window, beside the rail', (tester, db) async {
      await pumpApp(tester, db, width: 1000, location: '/settings/about');
      expect(find.byType(NavigationRail), findsOneWidget);
      expect(find.text('Based on ${Brand.upstreamName}'), findsOneWidget);
    });
  });

  group('Inbox', () {
    testApp('lists every message newest first, and opens its topic', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, height: 2400, location: '/inbox');

      final texts = tester
          .widgetList<Text>(find.byType(Text))
          .map((t) => t.data)
          .whereType<String>()
          .toList();
      expect(
        texts.indexOf('Disk almost full'),
        lessThan(texts.indexOf('Nightly backup finished')),
      );
      expect(
        texts.indexOf('Nightly backup finished'),
        lessThan(texts.indexOf('api v2.14.0')),
      );

      await tester.tap(find.text('Nightly backup finished'));
      await settle(tester);
      expect(find.text('2.1 GB in 4 minutes'), findsOneWidget);
      expect(find.byType(BackButton), findsOneWidget);
    });

    testApp('says when there is nothing', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/inbox');
      expect(find.text('No messages yet.'), findsOneWidget);
    }, seeded: false);
  });
}
