import 'dart:convert';

import 'package:drift/drift.dart';

part 'database.g.dart';

/// Stores a message's tags as a JSON array.
class StringListConverter extends TypeConverter<List<String>, String> {
  const StringListConverter();

  @override
  List<String> fromSql(String fromDb) =>
      List<String>.from(jsonDecode(fromDb) as List<dynamic>);

  @override
  String toSql(List<String> value) => jsonEncode(value);
}

/// A server the user subscribes through.
@DataClassName('ServerRow')
class Servers extends Table {
  IntColumn get id => integer().autoIncrement()();

  /// `host` or `host:port`, without the default port. Also the `:server` part of routes.
  TextColumn get host => text().unique()();

  /// Base URL without a trailing slash, for example `https://example.com`.
  TextColumn get url => text()();

  /// The `ETag` of the last list of themes the server sent, to ask whether it has changed.
  TextColumn get themesTag => text().nullable()();

  DateTimeColumn get createdAt => dateTime().withDefault(currentDateAndTime)();
}

/// A subscription. [cursor] and [readMarker] hold message IDs, which sort by time.
@DataClassName('TopicRow')
class Topics extends Table {
  IntColumn get id => integer().autoIncrement()();
  IntColumn get serverId =>
      integer().references(Servers, #id, onDelete: KeyAction.cascade)();
  TextColumn get name => text()();
  BoolColumn get muted => boolean().withDefault(const Constant(false))();

  /// Messages below this priority are stored but do not notify.
  IntColumn get minPriority => integer().withDefault(const Constant(1))();

  /// The newest message received. The connection resumes after it.
  TextColumn get cursor => text().nullable()();

  /// The newest message the user has seen. Newer ones are unread.
  TextColumn get readMarker => text().nullable()();

  DateTimeColumn get createdAt => dateTime().withDefault(currentDateAndTime)();

  @override
  List<Set<Column<Object>>> get uniqueKeys => [
    {serverId, name},
  ];
}

/// A received message, identified by its server and ID.
@DataClassName('MessageRow')
@TableIndex(name: 'messages_by_topic', columns: {#serverId, #topic, #id})
class Messages extends Table {
  IntColumn get serverId =>
      integer().references(Servers, #id, onDelete: KeyAction.cascade)();
  TextColumn get id => text()();
  TextColumn get topic => text()();

  /// Unix time in seconds, as the server sends it.
  IntColumn get time => integer()();
  TextColumn get title => text().nullable()();
  TextColumn get body => text().withDefault(const Constant(''))();
  IntColumn get priority => integer().withDefault(const Constant(3))();
  TextColumn get tags => text()
      .map(const StringListConverter())
      .withDefault(const Constant('[]'))();
  TextColumn get click => text().nullable()();

  /// Sent from this app by the user, so the thread shows it as theirs.
  BoolColumn get mine => boolean().withDefault(const Constant(false))();

  @override
  Set<Column<Object>> get primaryKey => {serverId, id};
}

/// The user's choices that are not about one server or topic, as text by name. See
/// `AppSettingsRepository` for the names and what they mean.
@DataClassName('SettingRow')
class AppSettings extends Table {
  TextColumn get key => text()();
  TextColumn get value => text()();

  @override
  Set<Column<Object>> get primaryKey => {key};
}

/// A theme a server offers, kept so the list is there offline. [json] is the theme as the server
/// sent it: `{"name", "brightness", "colors"}`.
@DataClassName('ServerThemeRow')
class ServerThemes extends Table {
  IntColumn get serverId =>
      integer().references(Servers, #id, onDelete: KeyAction.cascade)();

  /// The name in lower case, so "Midnight" and "midnight" are one theme.
  TextColumn get nameKey => text()();
  TextColumn get json => text()();

  @override
  Set<Column<Object>> get primaryKey => {serverId, nameKey};
}

@DriftDatabase(tables: [Servers, Topics, Messages, AppSettings, ServerThemes])
class AppDatabase extends _$AppDatabase {
  AppDatabase(super.e);

  /// 1: servers, topics, messages. 2: the settings table. 3: which messages are the user's own.
  /// 4: the themes servers offer.
  @override
  int get schemaVersion => 4;

  @override
  MigrationStrategy get migration => MigrationStrategy(
    onUpgrade: (migrator, from, to) async {
      if (from < 2) await migrator.createTable(appSettings);
      if (from < 3) await migrator.addColumn(messages, messages.mine);
      if (from < 4) {
        await migrator.addColumn(servers, servers.themesTag);
        await migrator.createTable(serverThemes);
      }
    },
    beforeOpen: (details) async {
      await customStatement('PRAGMA foreign_keys = ON');
    },
  );
}
