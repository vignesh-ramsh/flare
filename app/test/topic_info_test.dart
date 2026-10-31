import 'package:app/brand/brand.g.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/features/thread/topic_info_view.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:qr_flutter/qr_flutter.dart';

import 'helpers.dart';

void main() {
  const alerts = '/topic/example.com/alerts-db';
  const info = '$alerts/info';
  const link = '${Brand.urlScheme}://subscribe/example.com/alerts-db';

  /// Scrolls the info until [target] shows, so it can be tapped.
  Future<void> reveal(WidgetTester tester, Finder target) async {
    await tester.ensureVisible(target);
    await tester.pump();
  }

  List<String> mockClipboard(WidgetTester tester) {
    final copied = <String>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          copied.add((call.arguments as Map)['text'] as String);
        }
        return null;
      },
    );
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        null,
      ),
    );
    return copied;
  }

  Future<List<TopicRow>> topicsOf(WidgetTester tester, AppDatabase db) async =>
      (await tester.runAsync(() => db.select(db.topics).get()))!;

  group('reaching it', () {
    for (final width in [390.0, 700.0, 1000.0]) {
      testApp('the thread has an info button at $width', (tester, db) async {
        await pumpApp(tester, db, width: width, location: alerts);
        await tester.tap(find.byTooltip('Topic info'));
        await settle(tester);
        expect(find.byType(TopicInfoView), findsOneWidget);
        expect(find.text('Topic info'), findsWidgets);
        // A page of its own, with a way back to the thread.
        await tester.tap(find.byType(BackButton));
        await settle(tester);
        expect(find.byType(TopicInfoView), findsNothing);
        expect(find.text('Disk almost full'), findsWidgets);
      });
    }

    testApp('the large layout shows it beside the thread, and can hide it', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 1400, location: alerts);
      expect(find.byTooltip('Topic info'), findsNothing);
      expect(find.byType(TopicInfoView), findsOneWidget);
      expect(find.text('Disk almost full'), findsWidgets);

      await tester.tap(find.byTooltip('Hide details'));
      await settle(tester);
      expect(find.byType(TopicInfoView), findsNothing);
    });

    testApp('the info route on a large window is the thread with its pane', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 1400, location: info);
      expect(find.byType(TopicInfoView), findsOneWidget);
      expect(find.text('Disk almost full'), findsWidgets);
    });

    testApp('a link straight to it works on a narrow window', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, location: info);
      expect(find.byType(TopicInfoView), findsOneWidget);
      // Nothing to go back to: the way back lands on the thread.
      await tester.tap(find.byType(BackButton));
      await settle(tester);
      expect(find.byType(TopicInfoView), findsNothing);
      expect(find.text('Disk almost full'), findsWidgets);
    });
  });

  group('what it says', () {
    testApp('the facts', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: info);
      expect(find.text('alerts-db'), findsOneWidget);
      expect(find.text('example.com'), findsWidgets);
      expect(find.text('3'), findsOneWidget);
      expect(find.text('On'), findsOneWidget);
    });

    testApp('says a muted topic is muted', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/deploys/info',
      );
      expect(find.text('Muted'), findsOneWidget);
    });

    testApp('the link, the address and a QR code that holds the link', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, location: info);
      await reveal(tester, find.byType(LinkQrCode));

      expect(find.text(link), findsOneWidget);
      expect(find.text('https://example.com/alerts-db'), findsOneWidget);
      final qr = tester.widget<LinkQrCode>(find.byType(LinkQrCode));
      expect(qr.data, link);
      expect(qr.label, 'QR code of the subscribe link for alerts-db');
      expect(
        tester.widget<QrImageView>(find.byType(QrImageView)).semanticsLabel,
        qr.label,
      );
      expect(
        find.text('Scan it with ${Brand.name} to subscribe to this topic.'),
        findsOneWidget,
      );
    });

    testApp('the QR code stays dark on white in a dark theme', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: info,
        brightness: Brightness.dark,
      );
      await reveal(tester, find.byType(LinkQrCode));
      final qr = tester.widget<QrImageView>(find.byType(QrImageView));
      expect(qr.backgroundColor, Colors.white);
      expect(qr.eyeStyle.color, Colors.black);
      expect(qr.dataModuleStyle.color, Colors.black);
    });

    testApp('the link of a server that only speaks http says so', (
      tester,
      db,
    ) async {
      await tester.runAsync(
        () =>
            TopicsRepository(db)
                .subscribe(ServerAddress.parse('http://10.0.0.2:8080')!, 'lan'),
      );
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/10.0.0.2%3A8080/lan/info',
      );
      await reveal(tester, find.byType(LinkQrCode));

      expect(
        find.text(
          '${Brand.urlScheme}://subscribe/10.0.0.2:8080/lan?scheme=http',
        ),
        findsOneWidget,
      );
      expect(find.text('http://10.0.0.2:8080/lan'), findsOneWidget);
      expect(
        tester.widget<LinkQrCode>(find.byType(LinkQrCode)).data,
        '${Brand.urlScheme}://subscribe/10.0.0.2:8080/lan?scheme=http',
      );
    });

    testApp('has nothing to share for a topic that is not there', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/nope/info',
      );
      expect(find.byType(LinkQrCode), findsNothing);
      expect(find.text('Unsubscribe'), findsNothing);
    });

    testApp('meets the accessibility guidelines', (tester, db) async {
      final handle = tester.ensureSemantics();
      await pumpApp(tester, db, width: 390, location: info);
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await expectLater(tester, meetsGuideline(textContrastGuideline));
      handle.dispose();
    });
  });

  group('copying', () {
    testApp('puts the link on the clipboard, and says so', (tester, db) async {
      final copied = mockClipboard(tester);
      await pumpApp(tester, db, width: 390, location: info);
      await reveal(tester, find.byTooltip('Copy: Subscribe link'));
      await tester.tap(find.byTooltip('Copy: Subscribe link'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));

      expect(copied, [link]);
      expect(find.text('Copied'), findsOneWidget);
    });

    testApp('puts the address on the clipboard', (tester, db) async {
      final copied = mockClipboard(tester);
      await pumpApp(tester, db, width: 390, location: info);
      await reveal(tester, find.byTooltip('Copy: Topic address'));
      await tester.tap(find.byTooltip('Copy: Topic address'));
      await tester.pump();

      expect(copied, ['https://example.com/alerts-db']);
    });
  });

  group('unsubscribing', () {
    Future<void> press(WidgetTester tester) async {
      await reveal(tester, find.widgetWithText(OutlinedButton, 'Unsubscribe'));
      await tester.tap(find.widgetWithText(OutlinedButton, 'Unsubscribe'));
      await settle(tester);
      await tester.pump(const Duration(milliseconds: 300));
    }

    testApp('asks first, and says what will be deleted', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: info);
      await press(tester);

      expect(find.text('Unsubscribe from alerts-db?'), findsOneWidget);
      expect(
        find.textContaining('its 3 messages stored on this device are deleted'),
        findsOneWidget,
      );
      expect(
        find.textContaining('Nothing is deleted on the server.'),
        findsOneWidget,
      );
    });

    testApp('says the singular for one message', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/backups/info',
      );
      await press(tester);
      expect(
        find.textContaining('its 1 message stored on this device is deleted'),
        findsOneWidget,
      );
    });

    testApp('says there is nothing to delete for an empty topic', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/quiet/info',
      );
      await press(tester);
      expect(
        find.textContaining('You will stop getting messages from this topic.'),
        findsOneWidget,
      );
      expect(find.textContaining('deleted.'), findsNothing);
    });

    testApp('Cancel keeps the topic and its messages', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: info);
      await press(tester);
      await tester.tap(find.text('Cancel'));
      await settle(tester);
      await tester.pump(const Duration(milliseconds: 300));

      expect(await topicsOf(tester, db), hasLength(4));
      expect(
        (await tester.runAsync(() => db.select(db.messages).get()))!,
        hasLength(5),
      );
      expect(find.byType(TopicInfoView), findsOneWidget);
    });

    testApp('removes the topic and its messages, and goes to the list', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, location: info);
      await press(tester);
      await tester.tap(find.widgetWithText(FilledButton, 'Unsubscribe'));
      await settle(tester);
      await tester.pump(const Duration(milliseconds: 300));

      final names = (await topicsOf(tester, db)).map((t) => t.name);
      expect(names, unorderedEquals(['backups', 'deploys', 'quiet']));
      final left = (await tester.runAsync(() => db.select(db.messages).get()))!;
      expect(left.where((m) => m.topic == 'alerts-db'), isEmpty);
      expect(left, hasLength(2));

      expect(find.byType(TopicInfoView), findsNothing);
      expect(find.text('backups'), findsWidgets);
      expect(find.text('alerts-db'), findsNothing);
      expect(find.text('Unsubscribed from alerts-db'), findsOneWidget);
    });

    testApp('works from the details pane of a large window', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 1400, location: alerts);
      await press(tester);
      await tester.tap(find.widgetWithText(FilledButton, 'Unsubscribe'));
      await settle(tester);
      await tester.pump(const Duration(milliseconds: 300));

      expect(
        (await topicsOf(tester, db)).map((t) => t.name),
        isNot(contains('alerts-db')),
      );
      expect(find.byType(TopicInfoView), findsNothing);
    });
  });
}
