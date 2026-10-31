import 'package:app/core/db/database.dart';
import 'package:app/data/models.dart';
import 'package:drift/native.dart';

/// A fresh in-memory database.
AppDatabase memoryDb() => AppDatabase(NativeDatabase.memory());

/// Message IDs that sort by their number, like UUIDv7s sort by time.
String mid(int n) => '01933c3e-0000-7000-8000-${n.toString().padLeft(12, '0')}';

IncomingMessage incoming(
  int serverId,
  String topic,
  int n, {
  String? title,
  String body = 'text',
  int priority = 3,
  int? time,
  List<String> tags = const [],
  String? click,
}) {
  return IncomingMessage(
    serverId: serverId,
    id: mid(n),
    topic: topic,
    time: time ?? 1_700_000_000 + n,
    title: title,
    body: body,
    priority: priority,
    tags: tags,
    click: click,
  );
}
