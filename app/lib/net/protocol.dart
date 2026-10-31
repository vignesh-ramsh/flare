import 'dart:convert';

import '../data/models.dart';

/// Error codes the server puts in an `error` frame (api/openapi.yaml, `Error`).
abstract final class ErrorCode {
  static const invalidTopic = 40001;
  static const unauthorized = 40101;
  static const invalidCredentials = 40102;
  static const forbidden = 40301;
  static const alreadySubscribed = 40901;
  static const tooMany = 42901;

  /// A subscription was dropped because this client fell behind.
  static const lagged = 50002;
}

/// A frame the server sends over `/v1/ws`.
sealed class ServerFrame {
  const ServerFrame();
}

final class AuthenticatedFrame extends ServerFrame {
  const AuthenticatedFrame(this.user);

  final String user;
}

/// The subscription is live: from now on no message is missed. The backlog
/// follows it, so this does not mean the app has caught up.
final class SubscribedFrame extends ServerFrame {
  const SubscribedFrame(this.topic);

  final String topic;
}

final class UnsubscribedFrame extends ServerFrame {
  const UnsubscribedFrame(this.topic);

  final String topic;
}

/// A message, in the shape of the `Message` schema.
final class MessageFrame extends ServerFrame {
  const MessageFrame({
    required this.id,
    required this.topic,
    required this.time,
    this.title,
    this.body = '',
    this.priority = 3,
    this.tags = const [],
    this.click,
  });

  final String id;
  final String topic;

  /// Unix time in seconds.
  final int time;
  final String? title;
  final String body;
  final int priority;
  final List<String> tags;
  final String? click;

  IncomingMessage toIncoming(int serverId, {bool silent = false}) =>
      IncomingMessage(
        serverId: serverId,
        id: id,
        topic: topic,
        time: time,
        title: title,
        body: body,
        priority: priority,
        tags: tags,
        click: click,
        silent: silent,
      );
}

/// Something was refused. The connection stays open.
final class ErrorFrame extends ServerFrame {
  const ErrorFrame({required this.code, required this.message, this.topic});

  final int code;
  final String message;

  /// The topic it was about, if any.
  final String? topic;
}

/// A frame this app does not know. Servers may add events, so it is ignored.
final class UnknownFrame extends ServerFrame {
  const UnknownFrame(this.event);

  final String event;
}

/// Reads a text frame. Returns null if it is not a JSON object with an `event`,
/// or is a message missing what a message needs.
ServerFrame? parseFrame(String text) {
  final Object? decoded;
  try {
    decoded = jsonDecode(text);
  } on FormatException {
    return null;
  }
  if (decoded is! Map<String, dynamic>) return null;
  final frame = decoded;
  final event = frame['event'];
  if (event is! String) return null;

  String? string(String key) {
    final value = frame[key];
    return value is String ? value : null;
  }

  switch (event) {
    case 'authenticated':
      return AuthenticatedFrame(string('user') ?? '');
    case 'subscribed':
      final topic = string('topic');
      return topic == null ? null : SubscribedFrame(topic);
    case 'unsubscribed':
      final topic = string('topic');
      return topic == null ? null : UnsubscribedFrame(topic);
    case 'message':
      final id = string('id');
      final topic = string('topic');
      final time = frame['time'];
      if (id == null || id.isEmpty || topic == null || time is! int) {
        return null;
      }
      final tags = frame['tags'];
      final priority = frame['priority'];
      return MessageFrame(
        id: id,
        topic: topic,
        time: time,
        title: string('title'),
        body: string('message') ?? '',
        // The server leaves out the default priority.
        priority: priority is int && priority >= 1 && priority <= 5
            ? priority
            : 3,
        tags: tags is List
            ? [
                for (final t in tags)
                  if (t is String) t,
              ]
            : const [],
        click: string('click'),
      );
    case 'error':
      final code = frame['code'];
      if (code is! int) return null;
      return ErrorFrame(
        code: code,
        message: string('error') ?? '',
        topic: string('topic'),
      );
    default:
      return UnknownFrame(event);
  }
}

/// `{"op":"subscribe",...}`. [since] is a message ID, or `all` for everything the
/// server still has.
String subscribeFrame(String topic, String since) =>
    jsonEncode({'op': 'subscribe', 'topic': topic, 'since': since});

String unsubscribeFrame(String topic) =>
    jsonEncode({'op': 'unsubscribe', 'topic': topic});
