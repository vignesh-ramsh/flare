import 'package:app/brand/brand.g.dart';
import 'package:app/features/add/add_screen.dart';
import 'package:app/features/thread/details_pane.dart';
import 'package:app/features/topics/topic_list_pane.dart';
import 'package:app/features/topics/topic_tile.dart';
import 'package:app/ui/message_card.dart';
import 'package:app/ui/unread_badge.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'helpers.dart';

final _tiles = find.byType(TopicTile);
final _cards = find.byType(MessageCard);
final _bar = find.byType(NavigationBar);
final _rail = find.byType(NavigationRail);
final _fab = find.byType(FloatingActionButton);
final _sidebarName = find.text(Brand.name);

Finder _tile(String name) => find.widgetWithText(TopicTile, name);

Finder _badgeIn(Finder tile) =>
    find.descendant(of: tile, matching: find.byType(UnreadBadge));

void main() {
  group('compact (390 dp)', () {
    testApp('shows a bottom bar, one pane and a Subscribe button', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390);

      expect(_bar, findsOneWidget);
      expect(_rail, findsNothing);
      expect(_sidebarName, findsNothing);
      expect(_fab, findsOneWidget);
      expect(find.text('Subscribe'), findsOneWidget);
      expect(_tiles, findsNWidgets(4));
      expect(find.text('Select a topic'), findsNothing);
    });

    testApp(
      'lists the busiest topic first, with preview, time and unread count',
      (tester, db) async {
        await pumpApp(tester, db, width: 390);

        final first = tester.widget<TopicTile>(_tiles.first);
        expect(first.topic.name, 'alerts-db');
        expect(
          find.descendant(
            of: _tile('alerts-db'),
            matching: find.text('Disk almost full'),
          ),
          findsOneWidget,
        );
        expect(
          find.descendant(
            of: _tile('alerts-db'),
            matching: find.textContaining('11:06'),
          ),
          findsOneWidget,
        );
        expect(
          find.descendant(
            of: _badgeIn(_tile('alerts-db')),
            matching: find.text('3'),
          ),
          findsOneWidget,
        );
        expect(
          find.descendant(
            of: _tile('deploys'),
            matching: find.text('Yesterday'),
          ),
          findsOneWidget,
        );
      },
    );

    testApp('puts the time at the right edge, level with the unread badge', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390);

      final time = find.descendant(
        of: _tile('alerts-db'),
        matching: find.textContaining('11:06'),
      );
      final badge = _badgeIn(_tile('alerts-db'));
      expect(
        tester.getRect(time).right,
        closeTo(tester.getRect(badge).right, 0.5),
      );

      final other = find.descendant(
        of: _tile('backups'),
        matching: find.textContaining('9:00'),
      );
      expect(
        tester.getRect(other).right,
        closeTo(tester.getRect(time).right, 0.5),
      );
    });

    testApp('shows no badge for a read topic or a muted one, and marks muted', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390);

      expect(_badgeIn(_tile('backups')), findsNothing);
      expect(_badgeIn(_tile('deploys')), findsNothing);
      expect(
        find.descendant(
          of: _tile('deploys'),
          matching: find.byIcon(Icons.notifications_off_outlined),
        ),
        findsOneWidget,
      );
      expect(
        find.descendant(
          of: _tile('quiet'),
          matching: find.text('No messages yet'),
        ),
        findsOneWidget,
      );
    });

    testApp('counts unread topics on the Topics destination, not muted ones', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390);

      final badge = find.descendant(of: _bar, matching: find.byType(Badge));
      expect(
        find.descendant(of: badge, matching: find.text('3')),
        findsOneWidget,
      );
    });

    testApp(
      'opens a thread over the list, hides the bar, and reading clears the badge',
      (tester, db) async {
        await pumpApp(tester, db, width: 390);

        await tester.tap(_tile('alerts-db'));
        await settle(tester);

        expect(_cards, findsNWidgets(3));
        expect(_bar, findsNothing);
        expect(find.byType(BackButton), findsOneWidget);
        expect(find.byType(TopicListPane).hitTestable(), findsNothing);
        // The newest message is nearest the bottom of the screen.
        final newest = tester.getTopLeft(find.text('Disk almost full')).dy;
        final oldest = tester.getTopLeft(find.text('db-01 /var is at 70%')).dy;
        expect(
          newest,
          greaterThan(tester.getTopLeft(find.text('db-01 /var is at 85%')).dy),
        );
        expect(newest, greaterThan(oldest));

        await tester.tap(find.byType(BackButton));
        await settle(tester);

        expect(_bar, findsOneWidget);
        expect(_badgeIn(_tile('alerts-db')), findsNothing);
      },
    );

    testApp('shows priority as a word and a glyph on the card', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/alerts-db',
      );

      expect(find.text('High'), findsOneWidget);
      expect(find.byIcon(Icons.priority_high), findsOneWidget);
      expect(find.text('Low'), findsOneWidget);
      expect(find.byIcon(Icons.keyboard_arrow_down), findsOneWidget);
      expect(find.text('prod'), findsOneWidget);
    });

    testApp('a topic the user is not subscribed to says so', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/nope',
      );
      expect(
        find.text('You are not subscribed to this topic.'),
        findsOneWidget,
      );
    });

    testApp('switches destinations from the bar', (tester, db) async {
      await pumpApp(tester, db, width: 390);

      await tester.tap(find.descendant(of: _bar, matching: find.text('Inbox')));
      await settle(tester);
      expect(_cards, findsWidgets);
      expect(find.text('alerts-db on example.com'), findsWidgets);

      await tester.tap(
        find.descendant(of: _bar, matching: find.text('Settings')),
      );
      await settle(tester);
      expect(find.text('Servers'), findsOneWidget);
      expect(find.text('1 server'), findsOneWidget);
    });

    testApp('an empty database shows how to start', (tester, db) async {
      await pumpApp(tester, db, width: 390);

      expect(find.text('No topics yet'), findsOneWidget);
      expect(_tiles, findsNothing);
      expect(_fab, findsOneWidget);
    }, seeded: false);

    testApp('survives the largest text size without overflowing', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, textScale: 2);
      expect(tester.takeException(), isNull);
      expect(_tiles, findsWidgets);

      await tester.tap(_tile('alerts-db'));
      await settle(tester);
      expect(tester.takeException(), isNull);
      expect(_cards, findsWidgets);
    });

    testApp('renders in the dark theme', (tester, db) async {
      await pumpApp(tester, db, width: 390, brightness: Brightness.dark);
      expect(Theme.of(tester.element(_bar)).brightness, Brightness.dark);
      expect(_tiles, findsNWidgets(4));
    });
  });

  group('medium (700 dp)', () {
    testApp('shows a rail instead of the bar, still one pane at a time', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 700);

      expect(_rail, findsOneWidget);
      expect(_bar, findsNothing);
      expect(_fab, findsOneWidget);
      expect(find.text('Select a topic'), findsNothing);
      expect(_tiles, findsNWidgets(4));
    });

    testApp('opens a thread over the list and keeps the rail', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 700);

      await tester.tap(_tile('alerts-db'));
      await settle(tester);

      expect(_cards, findsNWidgets(3));
      expect(_rail, findsOneWidget);
      expect(find.byType(BackButton), findsOneWidget);
      expect(_tiles, findsNothing);
    });
  });

  group('expanded (1000 dp)', () {
    testApp('shows the rail and a 360 dp list beside a prompt', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 1000);

      expect(_rail, findsOneWidget);
      expect(tester.getSize(find.byType(TopicListPane)).width, 360);
      expect(find.text('Select a topic'), findsOneWidget);
      expect(_fab, findsNothing);
      expect(find.widgetWithIcon(IconButton, Icons.add), findsOneWidget);
    });

    testApp(
      'shows the list and the thread together, with the open topic selected',
      (tester, db) async {
        await pumpApp(tester, db, width: 1000);

        await tester.tap(_tile('alerts-db'));
        await settle(tester);

        expect(_tiles, findsNWidgets(4));
        expect(_cards, findsNWidgets(3));
        expect(find.text('Select a topic'), findsNothing);
        expect(find.byType(BackButton), findsNothing);
        expect(tester.widget<TopicTile>(_tile('alerts-db')).selected, isTrue);
        expect(tester.widget<TopicTile>(_tile('backups')).selected, isFalse);

        await tester.tap(_tile('backups'));
        await settle(tester);
        expect(_cards, findsNWidgets(1));
        expect(tester.widget<TopicTile>(_tile('backups')).selected, isTrue);
      },
    );

    testApp('subscribes in a dialog, not a sheet', (tester, db) async {
      await pumpApp(tester, db, width: 1000);

      await tester.tap(find.widgetWithIcon(IconButton, Icons.add));
      await settle(tester);

      expect(find.byType(Dialog), findsOneWidget);
      expect(find.byType(AddScreen), findsOneWidget);
    });
  });

  group('large (1400 dp)', () {
    testApp('shows the sidebar tree instead of a rail or a list', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 1400);

      expect(_sidebarName, findsOneWidget);
      expect(_rail, findsNothing);
      expect(_bar, findsNothing);
      expect(_fab, findsNothing);
      expect(find.byType(TopicListPane), findsNothing);
      expect(find.text('example.com'), findsOneWidget);
      for (final name in ['alerts-db', 'backups', 'deploys', 'quiet']) {
        expect(
          find.descendant(of: find.byType(ListTile), matching: find.text(name)),
          findsOneWidget,
        );
      }
      expect(find.text('Inbox'), findsOneWidget);
      expect(find.text('Settings'), findsOneWidget);
      expect(find.text('Select a topic'), findsOneWidget);
    });

    testApp('opens a thread from the sidebar, with a details pane', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 1400);

      await tester.tap(find.widgetWithText(ListTile, 'alerts-db'));
      await settle(tester);

      expect(_cards, findsNWidgets(3));
      expect(find.byType(DetailsPane), findsOneWidget);
      expect(tester.getSize(find.byType(DetailsPane)).width, 320);
      expect(
        find.descendant(
          of: find.byType(DetailsPane),
          matching: find.text('example.com'),
        ),
        findsOneWidget,
      );
      expect(
        find.descendant(of: find.byType(DetailsPane), matching: find.text('3')),
        findsOneWidget,
      );
      // The sidebar shows the topic as selected.
      expect(
        tester
            .widget<ListTile>(find.widgetWithText(ListTile, 'alerts-db'))
            .selected,
        isTrue,
      );
    });

    testApp('caps the thread at 720 dp and can hide the details pane', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 1400,
        location: '/topic/example.com/alerts-db',
      );

      expect(tester.getSize(_cards.first).width, lessThanOrEqualTo(720));

      await tester.tap(find.byTooltip('Hide details'));
      await settle(tester);
      expect(find.byType(DetailsPane), findsNothing);

      await tester.tap(find.byTooltip('Show details'));
      await settle(tester);
      expect(find.byType(DetailsPane), findsOneWidget);
    });

    testApp('subscribes from the sidebar in a dialog', (tester, db) async {
      await pumpApp(tester, db, width: 1400);

      await tester.tap(find.widgetWithText(FilledButton, 'Subscribe'));
      await settle(tester);

      expect(find.byType(Dialog), findsOneWidget);
    });
  });

  group('with no topics', () {
    testApp(
      'the large layout says how to start, and the button opens Subscribe',
      (tester, db) async {
        await pumpApp(tester, db, width: 1400);

        expect(find.text('No topics yet'), findsOneWidget);
        expect(find.text('Select a topic'), findsNothing);

        await tester.tap(find.widgetWithText(FilledButton, 'Subscribe').last);
        await settle(tester);
        expect(find.byType(Dialog), findsOneWidget);
      },
      seeded: false,
    );

    testApp('the expanded layout says it once, in the list', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 1000);

      expect(find.text('No topics yet'), findsOneWidget);
      expect(find.text('Select a topic'), findsOneWidget);
    }, seeded: false);
  });

  group('every width', () {
    for (final width in [390.0, 700.0, 1000.0, 1400.0]) {
      testApp(
        'lays out ${width.toInt()} dp without overflow, in light and dark',
        (tester, db) async {
          for (final brightness in Brightness.values) {
            await pumpApp(tester, db, width: width, brightness: brightness);
            expect(tester.takeException(), isNull, reason: '$brightness list');
            await tester.pumpWidget(const SizedBox.shrink());

            await pumpApp(
              tester,
              db,
              width: width,
              brightness: brightness,
              location: '/topic/example.com/alerts-db',
            );
            expect(
              tester.takeException(),
              isNull,
              reason: '$brightness thread',
            );
            await tester.pumpWidget(const SizedBox.shrink());
          }
        },
      );
    }

    testApp('keeps a short window usable', (tester, db) async {
      await pumpApp(tester, db, width: 1400, height: 400);
      expect(tester.takeException(), isNull);
    });
  });
}
