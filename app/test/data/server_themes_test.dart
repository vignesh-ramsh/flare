import 'dart:convert';

import 'package:app/app/themes/theme_spec.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/server_themes_repository.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/servers_repository.dart';
import 'package:drift/native.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../themes/theme_fixtures.dart';
import 'test_db.dart';

void main() {
  late AppDatabase db;
  late ServerThemesRepository themes;
  late ServersRepository servers;
  late int a;
  late int b;

  setUp(() async {
    db = memoryDb();
    themes = ServerThemesRepository(db);
    servers = ServersRepository(db);
    a = (await servers.add(ServerAddress.parse('https://a.example')!))
        .server
        .id;
    b = (await servers.add(ServerAddress.parse('https://b.example:8443')!))
        .server
        .id;
  });

  tearDown(() => db.close());

  ThemeSpec named(String name, {Brightness brightness = Brightness.dark}) =>
      ThemeSpec.seeded(
        name: name,
        brightness: brightness,
        primary: const Color(0xFF8AB4F8),
      );

  Future<Map<String, List<String>>> names() async => {
    for (final e in (await themes.watch().first).entries)
      e.key: [for (final t in e.value) t.name],
  };

  test('start empty', () async {
    expect(await themes.watch().first, isEmpty);
    expect(await themes.tagOf(a), isNull);
  });

  test(
    'a list is kept with the tag it came with, and comes back as it went in',
    () async {
      await themes.replace(a, [midnight(), paper()], '"abc"');
      final got = (await themes.watch().first)['a.example']!;
      expect(got, [midnight(), paper()]);
      expect(await themes.tagOf(a), '"abc"');
    },
  );

  test('themes are listed by name, under the server\'s host', () async {
    await themes.replace(a, [
      named('Zebra'),
      named('apple'),
      named('Mango'),
    ], null);
    await themes.replace(b, [named('Only')], null);
    expect(await names(), {
      'a.example': ['apple', 'Mango', 'Zebra'],
      'b.example:8443': ['Only'],
    });
  });

  test('a new list replaces the old one entirely, and the tag', () async {
    await themes.replace(a, [named('One'), named('Two')], '"1"');
    await themes.replace(a, [named('Three')], '"2"');
    expect(await names(), {
      'a.example': ['Three'],
    });
    expect(await themes.tagOf(a), '"2"');
  });

  test('one server\'s list does not touch another\'s', () async {
    await themes.replace(a, [named('Same')], '"a"');
    await themes.replace(b, [named('Same'), named('Other')], '"b"');
    await themes.replace(a, [], null);
    expect(await names(), {
      'b.example:8443': ['Other', 'Same'],
    });
    expect(await themes.tagOf(b), '"b"');
  });

  test('clearing forgets the themes and the tag', () async {
    await themes.replace(a, [named('One')], '"1"');
    await themes.clear(a);
    expect(await themes.watch().first, isEmpty);
    expect(await themes.tagOf(a), isNull);
  });

  test(
    'a name repeated in another case counts once, and the first stays',
    () async {
      await themes.replace(a, [
        named('Midnight'),
        named('MIDNIGHT', brightness: Brightness.light),
      ], null);
      final got = (await themes.watch().first)['a.example']!;
      expect(got, hasLength(1));
      expect(got.single.name, 'Midnight');
      expect(got.single.brightness, Brightness.dark);
    },
  );

  test('no more than the most are kept for a server', () async {
    final many = [
      for (var i = 0; i < ServerThemesRepository.maxPerServer + 20; i++)
        named('T${i.toString().padLeft(3, '0')}'),
    ];
    await themes.replace(a, many, null);
    expect(
      (await themes.watch().first)['a.example'],
      hasLength(ServerThemesRepository.maxPerServer),
    );
  });

  test('removing a server removes its themes with it', () async {
    await themes.replace(a, [named('One')], null);
    await themes.replace(b, [named('Two')], null);
    await servers.remove(a);
    expect(await names(), {
      'b.example:8443': ['Two'],
    });
    expect(await db.select(db.serverThemes).get(), hasLength(1));
  });

  test(
    'a theme stored in a form this version cannot read is left out, not fatal',
    () async {
      await themes.replace(a, [named('Good')], null);
      await db
          .into(db.serverThemes)
          .insert(
            ServerThemesCompanion.insert(
              serverId: a,
              nameKey: 'bad',
              json: '{"name":"bad"}',
            ),
          );
      await db
          .into(db.serverThemes)
          .insert(
            ServerThemesCompanion.insert(
              serverId: a,
              nameKey: 'junk',
              json: 'not json',
            ),
          );
      expect(await names(), {
        'a.example': ['Good'],
      });
    },
  );

  test('a theme with a colour from a newer version still opens, keeping the ones it knows', () async {
    await db
        .into(db.serverThemes)
        .insert(
          ServerThemesCompanion.insert(
            serverId: a,
            nameKey: 'future',
            json: jsonEncode({
              'name': 'Future',
              'brightness': 'dark',
              'colors': {'primary': '#112233', 'sparkle': '#FFFFFF'},
            }),
          ),
        );
    final got = (await themes.watch().first)['a.example']!.single;
    expect(got.colors.keys, [ThemeToken.primary]);
  });

  test('the list is told again when it changes', () async {
    final seen = <List<String>>[];
    final sub = themes.watch().listen(
      (m) => seen.add([
        for (final l in m.values)
          for (final t in l) t.name,
      ]),
    );
    await Future<void>.delayed(const Duration(milliseconds: 20));
    await themes.replace(a, [named('One')], null);
    await Future<void>.delayed(const Duration(milliseconds: 20));
    await themes.replace(a, [named('One'), named('Two')], null);
    await Future<void>.delayed(const Duration(milliseconds: 20));
    await sub.cancel();
    expect(seen.last, ['One', 'Two']);
    expect(seen, contains(equals(['One'])));
  });

  test(
    'a database from before themes existed is upgraded and keeps its data',
    () async {
      final old = AppDatabase(
        NativeDatabase.memory(
          setup: (raw) {
            raw.execute('''
CREATE TABLE servers (id INTEGER PRIMARY KEY AUTOINCREMENT, host TEXT NOT NULL UNIQUE,
  url TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)));
CREATE TABLE topics (id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE, name TEXT NOT NULL,
  muted INTEGER NOT NULL DEFAULT 0 CHECK (muted IN (0, 1)), min_priority INTEGER NOT NULL DEFAULT 1,
  cursor TEXT NULL, read_marker TEXT NULL,
  created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)), UNIQUE (server_id, name));
CREATE TABLE messages (server_id INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
  id TEXT NOT NULL, topic TEXT NOT NULL, time INTEGER NOT NULL, title TEXT NULL,
  body TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 3, tags TEXT NOT NULL DEFAULT '[]',
  click TEXT NULL, mine INTEGER NOT NULL DEFAULT 0 CHECK (mine IN (0, 1)), PRIMARY KEY (server_id, id));
CREATE TABLE app_settings ("key" TEXT NOT NULL PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO servers (host, url) VALUES ('example.com', 'https://example.com');
INSERT INTO app_settings ("key", value) VALUES ('appearance.theme', 'builtin:midnight');
INSERT INTO messages (server_id, id, topic, time, body, mine) VALUES (1, 'a1', 'alerts', 1700000000, 'kept', 1);
PRAGMA user_version = 3;
''');
          },
        ),
      );
      addTearDown(old.close);

      final repo = ServerThemesRepository(old);
      expect(await repo.tagOf(1), isNull);
      await repo.replace(1, [named('After')], '"t"');
      expect(await repo.tagOf(1), '"t"');
      expect((await repo.watch().first)['example.com']!.single.name, 'After');
      // Nothing that was there is lost.
      expect((await old.select(old.servers).getSingle()).host, 'example.com');
      expect(
        (await old.select(old.appSettings).getSingle()).value,
        'builtin:midnight',
      );
      final message = await old.select(old.messages).getSingle();
      expect((message.body, message.mine), ('kept', true));
    },
  );
}
