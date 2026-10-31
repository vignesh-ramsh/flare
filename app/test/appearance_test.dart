import 'package:app/app/theme.dart';
import 'package:app/app/themes/theme_providers.dart';
import 'package:app/app/themes/theme_spec.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/server_themes_repository.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/servers_repository.dart';
import 'package:app/net/theme_sync.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:flutter_test/flutter_test.dart';

import 'helpers.dart';
import 'themes/theme_fixtures.dart';

/// A sync that only says it was asked, so no test reaches a network.
class RecordingSync extends ThemeSync {
  RecordingSync(super.ref);

  final List<Duration> asked = [];

  @override
  Future<void> refreshAll({
    Duration within = const Duration(minutes: 1),
  }) async => asked.add(within);
}

void main() {
  const screen = '/settings/appearance';
  late RecordingSync sync;

  List<Override> themes({
    List<ThemeSpec>? builtin,
    Map<String, List<ThemeSpec>>? servers = const {},
  }) => [
    builtinThemesProvider.overrideWithValue(builtin ?? [paper(), midnight()]),
    // Without `servers`, the themes come from the database like the app's own.
    if (servers != null) serverThemesProvider.overrideWithValue(servers),
    themeSyncProvider.overrideWith((ref) => sync = RecordingSync(ref)),
  ];

  Future<String?> saved(WidgetTester tester, AppDatabase db) async {
    final rows = (await tester.runAsync(
      () => db.select(db.appSettings).get(),
    ))!;
    return rows
        .where((r) => r.key == 'appearance.theme')
        .map((r) => r.value)
        .firstOrNull;
  }

  MaterialApp app(WidgetTester tester) =>
      tester.widget<MaterialApp>(find.byType(MaterialApp));

  /// What the radio group has selected: `system`, or a theme's id.
  String? selected(WidgetTester tester) => tester
      .widget<RadioGroup<String>>(find.byType(RadioGroup<String>))
      .groupValue;

  Color pageColor(WidgetTester tester) =>
      Theme.of(tester.element(find.byType(Scaffold).first))
          .scaffoldBackgroundColor;

  Future<void> choose(WidgetTester tester, String name) async {
    await tester.tap(find.text(name));
    await settle(tester);
  }

  group('reaching it', () {
    testApp('Settings has an Appearance entry that opens it', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/settings',
        overrides: themes(),
      );
      await tester.tap(find.text('Appearance'));
      await settle(tester);
      expect(find.text('Follow the system'), findsOneWidget);
    });

    testApp('and a way back to Settings', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(),
      );
      await tester.tap(find.byType(BackButton));
      await settle(tester);
      expect(find.text('Appearance'), findsOneWidget);
      expect(find.text('Follow the system'), findsNothing);
    });
  });

  group('what it lists', () {
    testApp(
      'follow the system, chosen, and the themes built in, each light or dark',
      (tester, db) async {
        await pumpApp(
          tester,
          db,
          width: 390,
          location: screen,
          overrides: themes(),
        );
        expect(selected(tester), 'system');
        expect(find.text('Themes in this app'), findsOneWidget);
        expect(
          find.widgetWithText(RadioListTile<String>, 'Paper'),
          findsOneWidget,
        );
        expect(
          find.widgetWithText(RadioListTile<String>, 'Midnight'),
          findsOneWidget,
        );
        expect(
          find.descendant(
            of: find.widgetWithText(RadioListTile<String>, 'Paper'),
            matching: find.text('Light'),
          ),
          findsOneWidget,
        );
        expect(
          find.descendant(
            of: find.widgetWithText(RadioListTile<String>, 'Midnight'),
            matching: find.text('Dark'),
          ),
          findsOneWidget,
        );
      },
    );

    testApp('says servers can offer more, when none does', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(),
      );
      expect(
        find.text('Servers you use can offer more themes.'),
        findsOneWidget,
      );
    });

    testApp('lists a server\'s themes under that server', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(
          servers: {
            'example.com': [fullTheme(name: 'Meadow')],
          },
        ),
      );
      expect(find.text('Themes from example.com'), findsOneWidget);
      expect(
        find.widgetWithText(RadioListTile<String>, 'Meadow'),
        findsOneWidget,
      );
      expect(find.text('Servers you use can offer more themes.'), findsNothing);
    });

    testApp(
      'has only the choice to follow the system when the app has no themes',
      (tester, db) async {
        await pumpApp(
          tester,
          db,
          width: 390,
          location: screen,
          overrides: themes(builtin: const []),
        );
        expect(find.byType(RadioListTile<String>), findsOneWidget);
        expect(find.text('Themes in this app'), findsNothing);
      },
    );

    testApp('leaves out a theme nobody could read', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(builtin: [paper(), unreadable()]),
      );
      expect(find.text('Murky'), findsNothing);
      expect(find.text('Paper'), findsOneWidget);
    });
  });

  group('choosing', () {
    testApp(
      'applies the theme at once, light or dark whatever the device is set to',
      (tester, db) async {
        await pumpApp(
          tester,
          db,
          width: 390,
          location: screen,
          overrides: themes(),
        );
        final before = pageColor(tester);

        await choose(tester, 'Midnight');
        expect(pageColor(tester), const Color(0xFF0B1220));
        expect(pageColor(tester), isNot(before));
        expect(app(tester).themeMode, ThemeMode.dark);
        expect(
          Theme.of(tester.element(find.byType(Scaffold).first))
              .colorScheme
              .brightness,
          Brightness.dark,
        );

        await choose(tester, 'Paper');
        expect(app(tester).themeMode, ThemeMode.light);
        expect(
          Theme.of(tester.element(find.byType(Scaffold).first))
              .colorScheme
              .primary,
          const Color(0xFF455A64),
        );
      },
    );

    testApp('remembers the choice', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(),
      );
      await choose(tester, 'Midnight');
      expect(await saved(tester, db), 'builtin:midnight');
      expect(selected(tester), 'builtin:midnight');
    });

    testApp('starts with the remembered theme', (tester, db) async {
      await tester.runAsync(
        () => db
            .into(db.appSettings)
            .insert(
              AppSettingsCompanion.insert(
                key: 'appearance.theme',
                value: 'builtin:midnight',
              ),
            ),
      );
      await pumpApp(tester, db, width: 390, location: '/', overrides: themes());
      expect(pageColor(tester), const Color(0xFF0B1220));
      expect(app(tester).themeMode, ThemeMode.dark);
    });

    testApp('goes back to following the system', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(),
      );
      await choose(tester, 'Midnight');
      await choose(tester, 'Follow the system');

      expect(await saved(tester, db), isNull);
      expect(app(tester).themeMode, ThemeMode.system);
      expect(
        pageColor(tester),
        buildTheme(Brightness.light).scaffoldBackgroundColor,
      );
    });

    testApp('a server\'s theme is chosen and remembered as that server\'s', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(
          servers: {
            'example.com': [fullTheme(name: 'Meadow')],
          },
        ),
      );
      await choose(tester, 'Meadow');
      expect(await saved(tester, db), 'server:example.com:meadow');
      expect(pageColor(tester), const Color(0xFFF1F8E9));
    });

    testApp('the same name from two places stays two choices', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(
          builtin: [paper()],
          servers: {
            'example.com': [paper()],
          },
        ),
      );
      expect(
        find.widgetWithText(RadioListTile<String>, 'Paper'),
        findsNWidgets(2),
      );
      await tester.tap(
        find.widgetWithText(RadioListTile<String>, 'Paper').last,
      );
      await settle(tester);
      expect(await saved(tester, db), 'server:example.com:paper');
    });
  });

  group('themes a server offers, kept on the device', () {
    Future<void> cache(
      WidgetTester tester,
      AppDatabase db,
      String host,
      List<ThemeSpec> specs,
    ) async {
      await tester.runAsync(() async {
        final server = await (db.select(
          db.servers,
        )..where((t) => t.host.equals(host))).getSingle();
        await ServerThemesRepository(db).replace(server.id, specs, '"t"');
      });
    }

    testApp('are listed under their server, from the database', (
      tester,
      db,
    ) async {
      await cache(tester, db, 'example.com', [fullTheme(name: 'Meadow')]);
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(servers: null),
      );
      expect(find.text('Themes from example.com'), findsOneWidget);
      expect(
        find.widgetWithText(RadioListTile<String>, 'Meadow'),
        findsOneWidget,
      );
    });

    testApp('are chosen, kept and used like the built-in ones', (
      tester,
      db,
    ) async {
      await cache(tester, db, 'example.com', [fullTheme(name: 'Meadow')]);
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(servers: null),
      );
      await choose(tester, 'Meadow');
      expect(await saved(tester, db), 'server:example.com:meadow');
      expect(pageColor(tester), const Color(0xFFF1F8E9));
    });

    testApp('ask the servers again when the screen opens, and not in a hurry', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(servers: null),
      );
      expect(sync.asked, [const Duration(seconds: 10)]);
    });

    testApp('appear while the screen is open, when a server\'s list arrives', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(servers: null),
      );
      expect(find.text('Meadow'), findsNothing);
      await cache(tester, db, 'example.com', [fullTheme(name: 'Meadow')]);
      await settle(tester);
      expect(
        find.widgetWithText(RadioListTile<String>, 'Meadow'),
        findsOneWidget,
      );
    });

    testApp('the chosen one going away leaves the app following the system', (
      tester,
      db,
    ) async {
      await cache(tester, db, 'example.com', [fullTheme(name: 'Meadow')]);
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(servers: null),
      );
      await choose(tester, 'Meadow');
      expect(app(tester).themeMode, ThemeMode.light);

      await cache(tester, db, 'example.com', const []);
      await settle(tester);
      expect(find.text('Meadow'), findsNothing);
      expect(app(tester).themeMode, ThemeMode.system);
      expect(selected(tester), 'system');
    });

    testApp('from two servers stay apart, and so do equal names', (
      tester,
      db,
    ) async {
      await tester.runAsync(
        () =>
            ServersRepository(db)
                .add(ServerAddress.parse('https://other.example')!),
      );
      await cache(tester, db, 'example.com', [paper()]);
      await cache(tester, db, 'other.example', [paper()]);
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(builtin: const [], servers: null),
      );
      expect(find.text('Themes from example.com'), findsOneWidget);
      expect(find.text('Themes from other.example'), findsOneWidget);
      expect(
        find.widgetWithText(RadioListTile<String>, 'Paper'),
        findsNWidgets(2),
      );
    });

    testApp(
      'with no server themes on the device, the app asks for none and shows none',
      (tester, db) async {
        await pumpApp(
          tester,
          db,
          width: 390,
          location: screen,
          overrides: themes(servers: null),
        );
        expect(find.textContaining('Themes from'), findsNothing);
      },
    );
  });

  group('a theme that goes away', () {
    testApp('falls back to following the system, and says nothing is chosen', (
      tester,
      db,
    ) async {
      await tester.runAsync(
        () => db
            .into(db.appSettings)
            .insert(
              AppSettingsCompanion.insert(
                key: 'appearance.theme',
                value: 'server:gone.example:meadow',
              ),
            ),
      );
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(),
      );
      expect(app(tester).themeMode, ThemeMode.system);
      expect(selected(tester), 'system');
    });

    testApp('and a saved choice that is not a theme is ignored', (
      tester,
      db,
    ) async {
      await tester.runAsync(
        () => db
            .into(db.appSettings)
            .insert(
              AppSettingsCompanion.insert(
                key: 'appearance.theme',
                value: 'nonsense',
              ),
            ),
      );
      await pumpApp(tester, db, width: 390, location: '/', overrides: themes());
      expect(app(tester).themeMode, ThemeMode.system);
    });
  });

  group('the app in a chosen theme', () {
    for (final width in [390.0, 700.0, 1000.0, 1400.0]) {
      testApp('lays out at $width without overflow', (tester, db) async {
        await tester.runAsync(
          () => db
              .into(db.appSettings)
              .insert(
                AppSettingsCompanion.insert(
                  key: 'appearance.theme',
                  value: 'builtin:midnight',
                ),
              ),
        );
        await pumpApp(
          tester,
          db,
          width: width,
          location: '/topic/example.com/alerts-db',
          overrides: themes(),
        );
        expect(pageColor(tester), const Color(0xFF0B1220));
        expect(find.text('Disk almost full'), findsWidgets);
      });
    }

    testApp(
      'the unread badge takes the theme\'s accent, not the brand colour',
      (tester, db) async {
        await tester.runAsync(
          () => db
              .into(db.appSettings)
              .insert(
                AppSettingsCompanion.insert(
                  key: 'appearance.theme',
                  value: 'builtin:midnight',
                ),
              ),
        );
        await pumpApp(tester, db, width: 390, overrides: themes());
        final badge = tester.widget<Badge>(find.byType(Badge).first);
        expect(badge.backgroundColor, const Color(0xFF8AB4F8));
        expect(badge.textColor, onColor(const Color(0xFF8AB4F8)));
      },
    );

    testApp('the unread badge is the brand colour in the default theme', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, overrides: themes());
      expect(
        tester.widget<Badge>(find.byType(Badge).first).backgroundColor,
        brandColor,
      );
    });

    testApp(
      'Appearance itself meets the accessibility guidelines in a dark theme',
      (tester, db) async {
        final handle = tester.ensureSemantics();
        await tester.runAsync(
          () => db
              .into(db.appSettings)
              .insert(
                AppSettingsCompanion.insert(
                  key: 'appearance.theme',
                  value: 'builtin:midnight',
                ),
              ),
        );
        await pumpApp(
          tester,
          db,
          width: 390,
          location: screen,
          overrides: themes(),
        );
        await expectLater(tester, meetsGuideline(textContrastGuideline));
        await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
        await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
        handle.dispose();
      },
    );

    testApp('and in the default theme', (tester, db) async {
      final handle = tester.ensureSemantics();
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(),
      );
      await expectLater(tester, meetsGuideline(textContrastGuideline));
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
      handle.dispose();
    });

    testApp('a theme\'s name and its brightness are read together', (
      tester,
      db,
    ) async {
      final handle = tester.ensureSemantics();
      await pumpApp(
        tester,
        db,
        width: 390,
        location: screen,
        overrides: themes(),
      );
      expect(find.bySemanticsLabel(RegExp('Midnight')), findsWidgets);
      handle.dispose();
    });
  });
}
