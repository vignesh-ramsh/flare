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

  @override
  Set<Column<Object>> get primaryKey => {serverId, id};
}

@DriftDatabase(tables: [Servers, Topics, Messages])
class AppDatabase extends _$AppDatabase {
  AppDatabase(super.e);

  @override
  int get schemaVersion => 1;

  @override
  MigrationStrategy get migration => MigrationStrategy(
    beforeOpen: (details) async {
      await customStatement('PRAGMA foreign_keys = ON');
    },
  );
}
