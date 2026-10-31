import '../data/models.dart';
import '../l10n/app_localizations.dart';
import '../ui/priority.dart';
import 'notification_policy.dart';

/// What the sink shows, before a platform puts it in its own shape.
class ShownNotification {
  const ShownNotification({
    required this.id,
    required this.title,
    required this.body,
    required this.level,
    required this.sound,
    required this.route,
    this.subtitle,
  });

  /// Names the notification, so showing it again replaces it.
  final int id;
  final String title;

  /// Where it came from, such as `alerts on example.com`. Platforms with no place for it put it
  /// above the body.
  final String? subtitle;
  final String body;
  final NotificationLevel level;
  final bool sound;

  /// The app route to open when the notification is tapped.
  final String route;
}

/// A stable positive number for a string, for notification IDs (31 bits: some platforms
/// take 32-bit signed integers).
int notificationId(String text) {
  var hash = 0x811C9DC5;
  for (final unit in text.codeUnits) {
    hash = ((hash ^ unit) * 0x01000193) & 0xFFFFFFFF;
  }
  return hash & 0x7FFFFFFF;
}

/// The notification for one message.
///
/// [showContent] off keeps the text out of it: it only says a message arrived on the topic.
ShownNotification contentForMessage({
  required AppLocalizations l10n,
  required IncomingMessage message,
  required String host,
  required String route,
  required NotificationLevel level,
  required bool sound,
  required bool showContent,
}) {
  final word = priorityWord(l10n, message.priority);
  final hasTitle = message.title != null && message.title!.isNotEmpty;

  final String title;
  final String body;
  if (!showContent) {
    title = message.topic;
    body = l10n.notificationNewMessage;
  } else {
    final base = hasTitle ? message.title! : message.topic;
    // The word, because priority is never shown by a glyph or a colour alone.
    title = word == null ? base : '$word · $base';
    body = message.body;
  }
  return ShownNotification(
    id: notificationId('${message.serverId}/${message.id}'),
    title: title,
    subtitle: showContent && !hasTitle
        ? host
        : l10n.topicOnServer(message.topic, host),
    body: body,
    level: level,
    sound: sound && level != NotificationLevel.quiet,
    route: route,
  );
}

/// One notification for a burst of messages on a topic, such as what piled up while the app
/// was away: [count] of them, at the most prominent [level] among them.
ShownNotification contentForBurst({
  required AppLocalizations l10n,
  required int serverId,
  required String topic,
  required String host,
  required String route,
  required int count,
  required NotificationLevel level,
  required bool sound,
  required String newestId,
}) {
  return ShownNotification(
    id: notificationId('$serverId/$topic/burst/$newestId'),
    title: topic,
    subtitle: l10n.topicOnServer(topic, host),
    body: l10n.notificationManyMessages(count),
    level: level,
    sound: sound && level != NotificationLevel.quiet,
    route: route,
  );
}
