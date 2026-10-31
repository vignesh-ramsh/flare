import '../core/db/database.dart';

/// A subscription as the topic list shows it.
class TopicSummary {
  const TopicSummary({
    required this.topicId,
    required this.serverId,
    required this.host,
    required this.name,
    required this.muted,
    required this.minPriority,
    required this.unread,
    required this.messageCount,
    this.last,
  });

  final int topicId;
  final int serverId;
  final String host;
  final String name;
  final bool muted;
  final int minPriority;
  final int unread;
  final int messageCount;

  /// The newest message, if any.
  final LastMessage? last;
}

class LastMessage {
  const LastMessage({
    required this.id,
    required this.time,
    required this.priority,
    required this.body,
    this.title,
  });

  final String id;
  final DateTime time;
  final int priority;
  final String? title;
  final String body;

  /// The line a list shows: the title, or the text when there is none.
  String get preview => (title != null && title!.isNotEmpty) ? title! : body;
}

/// A message to store, as the connection delivers it.
class IncomingMessage {
  const IncomingMessage({
    required this.serverId,
    required this.id,
    required this.topic,
    required this.time,
    this.title,
    this.body = '',
    this.priority = 3,
    this.tags = const [],
    this.click,
  });

  final int serverId;
  final String id;
  final String topic;

  /// Unix time in seconds.
  final int time;
  final String? title;
  final String body;
  final int priority;
  final List<String> tags;
  final String? click;
}

/// A stored message and the topic it belongs to, for the inbox.
class InboxItem {
  const InboxItem({required this.message, required this.host});

  final MessageRow message;
  final String host;
}

/// Names a subscription in routes: the server's host and the topic.
typedef TopicRef = ({String host, String name});

enum SubscribeResult { added, alreadySubscribed }

/// A subscription and the server it is on.
class FollowedTopic {
  const FollowedTopic({required this.server, required this.topic});

  final ServerRow server;
  final String topic;
}
