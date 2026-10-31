@Timeout(Duration(seconds: 90))
library;

import 'package:app/app/themes/theme_registry.dart';
import 'package:app/app/themes/theme_spec.dart';
import 'package:app/auth/credentials.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/providers.dart';
import 'package:app/data/server_url.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/theme_sync.dart';
import 'package:drift/native.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../auth/memory_store.dart';
import 'real_server.dart';

/// The themes a real Flare server offers, through the app's own REST client and sync.
/// Skipped unless FLARE_SERVER_BIN is set.
void main() {
  group('themes from the real server', () {
    RealServer? server;
    ProviderContainer? container;
    AppDatabase? db;
    late String admin;

    tearDown(() async {
      container?.dispose();
      await db?.close();
      await server?.dispose();
      container = null;
      db = null;
      server = null;
    });

    Future<void> start({Map<String, String> env = const {}}) async {
      server = await RealServer.start(env: env);
      admin = await server!.addUserWithToken('root', admin: true);
      db = AppDatabase(NativeDatabase.memory());
      container = ProviderContainer(
        overrides: [
          databaseProvider.overrideWithValue(db!),
          secretStoreProvider.overrideWithValue(MemorySecretStore()),
        ],
      );
    }

    Future<(int, String)> post(Map<String, Object> theme) =>
        server!.admin('POST', '/v1/admin/themes', admin, theme);

    Map<String, Object> midnight() => {
      'name': 'Midnight',
      'brightness': 'dark',
      'colors': {
        'primary': '#8AB4F8',
        'background': '#0B1220',
        'surface': '#111A2B',
        'on_surface': '#E6EDF7',
      },
    };

    test(
      'a server with no themes offers none, and the app is told so',
      () async {
        await start();
        final answer = await ServerApi().themes(server!.url);
        expect(answer.changed, isTrue);
        expect(answer.themes, isEmpty);
        expect(answer.tag, isNotNull);
      },
    );

    test(
      'a theme an administrator adds reaches the app exactly as set',
      () async {
        await start();
        expect((await post(midnight())).$1, 201);

        final answer = await ServerApi().themes(server!.url);
        expect(answer.themes, hasLength(1));
        final theme = answer.themes.single;
        expect(theme.name, 'Midnight');
        expect(theme.brightness, Brightness.dark);
        expect(theme.colors[ThemeToken.primary], const Color(0xFF8AB4F8));
        expect(theme.colors[ThemeToken.background], const Color(0xFF0B1220));
        expect(isReadable(theme), isTrue);
      },
    );

    test('asking again with its tag is told nothing has changed, until something does', () async {
      await start();
      await post(midnight());
      final api = ServerApi();
      final first = await api.themes(server!.url);
      expect((await api.themes(server!.url, tag: first.tag)).changed, isFalse);

      await post({
        'name': 'Paper',
        'brightness': 'light',
        'colors': {'primary': '#2F5D8C'},
      });
      final next = await api.themes(server!.url, tag: first.tag);
      expect(next.changed, isTrue);
      expect(next.themes.map((t) => t.name), ['Midnight', 'Paper']);
      expect(next.tag, isNot(first.tag));
    });

    test('is public on a server that keeps its topics private', () async {
      await start(env: {'FLARE_AUTH_DEFAULT_ACCESS': 'deny-all'});
      await post(midnight());
      expect((await ServerApi().themes(server!.url)).themes, hasLength(1));
    });

    test(
      'a theme of one colour is made readable by the app, whatever the colour',
      () async {
        await start();
        for (final colour in [
          '#FFD60A',
          '#000000',
          '#FFFFFF',
          '#00FF00',
          '#0A1A3A',
        ]) {
          final name = 'T${colour.substring(1)}';
          expect(
            (await post({
              'name': name,
              'brightness': 'light',
              'colors': {'primary': colour},
            })).$1,
            201,
            reason: name,
          );
        }
        final themes = (await ServerApi().themes(server!.url)).themes;
        expect(themes, hasLength(5));
        expect(themes.every(isReadable), isTrue);
      },
    );

    test('the server refuses what the app would refuse to show', () async {
      await start();
      final (status, body) = await post({
        'name': 'Murky',
        'brightness': 'light',
        'colors': {
          'primary': '#455A64',
          'surface': '#808080',
          'on_surface': '#858585',
        },
      });
      expect(status, 400);
      expect(body, contains('contrast'));
      expect((await ServerApi().themes(server!.url)).themes, isEmpty);
    });

    test(
      'the app keeps them on the device and follows the server\'s changes',
      () async {
        await start();
        await post(midnight());
        await post({
          'name': 'Paper',
          'brightness': 'light',
          'colors': {'primary': '#2F5D8C'},
        });
        final server_ = await container!
            .read(serversRepositoryProvider)
            .add(ServerAddress.parse(server!.url.toString())!);
        final sync = container!.read(themeSyncProvider);
        final themes = container!.read(serverThemesRepositoryProvider);
        String host() => server_.server.host;

        await sync.refreshAll();
        expect(
          {
            for (final e in (await themes.watch().first).entries)
              e.key: e.value.map((t) => t.name).toList(),
          },
          {
            host(): ['Midnight', 'Paper'],
          },
        );

        // The administrator deletes one and adds another.
        expect(
          (await server!.admin('DELETE', '/v1/admin/themes/paper', admin)).$1,
          204,
        );
        await post({
          'name': 'Sepia',
          'brightness': 'light',
          'colors': {'primary': '#8B5A2B'},
        });
        await sync.refreshAll(within: Duration.zero);
        expect((await themes.watch().first)[host()]!.map((t) => t.name), [
          'Midnight',
          'Sepia',
        ]);

        // The server goes away: what the app holds stays.
        await server!.stop();
        await sync.refreshAll(within: Duration.zero);
        expect((await themes.watch().first)[host()]!.map((t) => t.name), [
          'Midnight',
          'Sepia',
        ]);
      },
    );

    test(
      'the available themes are built from what the server offered',
      () async {
        await start();
        await post(midnight());
        final added = await container!
            .read(serversRepositoryProvider)
            .add(ServerAddress.parse(server!.url.toString())!);
        await container!.read(themeSyncProvider).refreshAll();
        // Let the stream deliver, then ask what is on offer.
        final byHost = await container!
            .read(serverThemesRepositoryProvider)
            .watch()
            .first;
        final offered = collectThemes(builtin: const [], servers: byHost);
        expect(offered.map((t) => t.ref.id), [
          'server:${added.server.host}:midnight',
        ]);
      },
    );
  }, skip: serverBinary == null ? skipWithoutServer : false);
}
