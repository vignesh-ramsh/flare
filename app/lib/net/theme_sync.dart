import 'dart:developer' as developer;

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/db/database.dart';
import '../data/providers.dart';
import 'api_client.dart';
import 'providers.dart';

/// Keeps each server's themes on the device up to date: asks a server for its list when it is
/// added, when the app starts or returns to the foreground, and when Appearance opens.
///
/// Themes are public, so no sign-in is needed. The server's `ETag` is sent back each time, so an
/// unchanged list costs almost nothing. A server that cannot be reached keeps the list it had;
/// one that has no themes endpoint (an older server) has none.
class ThemeSync {
  ThemeSync(this._ref);

  final Ref _ref;
  final Map<int, DateTime> _lastSynced = {};
  final Set<int> _running = {};

  /// Asks every server, except those asked within [within].
  Future<void> refreshAll({
    Duration within = const Duration(minutes: 1),
  }) async {
    final servers = await _ref.read(serversRepositoryProvider).watchAll().first;
    await Future.wait([for (final s in servers) sync(s, within: within)]);
  }

  /// Asks one server for its themes, unless it was asked within [within] or is being asked now.
  Future<void> sync(
    ServerRow server, {
    Duration within = const Duration(minutes: 1),
  }) async {
    if (_running.contains(server.id)) return;
    final now = _ref.read(clockProvider)();
    final last = _lastSynced[server.id];
    if (last != null && now.difference(last) < within) return;

    _running.add(server.id);
    final themes = _ref.read(serverThemesRepositoryProvider);
    try {
      final tag = await themes.tagOf(server.id);
      final answer = await _ref
          .read(apiProvider)
          .themes(Uri.parse(server.url), tag: tag);
      if (answer.changed) {
        await themes.replace(server.id, answer.themes, answer.tag);
      }
      _lastSynced[server.id] = now;
    } on ApiException catch (error) {
      if (error.failure == ApiFailure.notSupported) {
        // The server does not offer themes (it may be an older one): it has none.
        await _forget(server.id);
        _lastSynced[server.id] = now;
      }
      // Any other failure (unreachable, slow, refused): keep the list held, try again later.
    } on Object catch (error) {
      // The server was removed while it was being asked, or the database is closing.
      developer.log('themes of ${server.host}: $error', name: 'themes');
    } finally {
      _running.remove(server.id);
    }
  }

  Future<void> _forget(int serverId) async {
    try {
      await _ref.read(serverThemesRepositoryProvider).clear(serverId);
    } on Object {
      // The server is gone, and its themes with it.
    }
  }

  /// Servers added after the app started are asked at once.
  void _serversChanged(
    AsyncValue<List<ServerRow>>? before,
    AsyncValue<List<ServerRow>> after,
  ) {
    final known = {for (final s in before?.value ?? const <ServerRow>[]) s.id};
    for (final server in after.value ?? const <ServerRow>[]) {
      if (!known.contains(server.id) && before?.value != null) sync(server);
    }
  }
}

final themeSyncProvider = Provider<ThemeSync>((ref) {
  final sync = ThemeSync(ref);
  ref.listen(serversProvider, sync._serversChanged);
  return sync;
});
