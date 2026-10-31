import 'dart:convert';

import 'package:drift/drift.dart';

import '../app/themes/theme_spec.dart';
import '../core/db/database.dart';

/// The themes each server offers, kept on the device so Appearance can list them offline.
class ServerThemesRepository {
  ServerThemesRepository(this._db);

  final AppDatabase _db;

  /// The most themes read from one server, whatever it sends.
  static const maxPerServer = 100;

  /// Every server's themes by the server's host, each list ordered by name. A theme that cannot
  /// be read (stored by a version that knew something this one does not) is left out.
  Stream<Map<String, List<ThemeSpec>>> watch() {
    final query = _db.select(_db.serverThemes).join([
      innerJoin(
        _db.servers,
        _db.servers.id.equalsExp(_db.serverThemes.serverId),
      ),
    ])..orderBy([OrderingTerm.asc(_db.serverThemes.nameKey)]);
    return query.watch().map((rows) {
      final byHost = <String, List<ThemeSpec>>{};
      for (final row in rows) {
        final spec = _parse(row.readTable(_db.serverThemes).json);
        if (spec == null) continue;
        byHost.putIfAbsent(row.readTable(_db.servers).host, () => []).add(spec);
      }
      return byHost;
    });
  }

  /// The `ETag` of the list last stored for the server, or null.
  Future<String?> tagOf(int serverId) async {
    final row = await (_db.select(
      _db.servers,
    )..where((s) => s.id.equals(serverId))).getSingleOrNull();
    return row?.themesTag;
  }

  /// Makes [themes] the server's list, in place of whatever was stored, with the [tag] the server
  /// sent with it. Names repeated in different case count once; the first stays.
  Future<void> replace(int serverId, List<ThemeSpec> themes, String? tag) {
    return _db.transaction(() async {
      await (_db.delete(
        _db.serverThemes,
      )..where((t) => t.serverId.equals(serverId))).go();
      final seen = <String>{};
      for (final spec in themes.take(maxPerServer)) {
        if (!seen.add(spec.nameKey)) continue;
        await _db
            .into(_db.serverThemes)
            .insert(
              ServerThemesCompanion.insert(
                serverId: serverId,
                nameKey: spec.nameKey,
                json: jsonEncode(spec.toJson()),
              ),
            );
      }
      await (_db.update(_db.servers)..where((s) => s.id.equals(serverId)))
          .write(ServersCompanion(themesTag: Value(tag)));
    });
  }

  /// Forgets the server's themes, as when it turns out not to offer any.
  Future<void> clear(int serverId) => replace(serverId, const [], null);

  ThemeSpec? _parse(String json) {
    try {
      return ThemeSpec.fromJson(jsonDecode(json));
    } on Object {
      return null;
    }
  }
}
