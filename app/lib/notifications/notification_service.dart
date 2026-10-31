import 'dart:async';
import 'dart:developer' as developer;

import '../app/routes.dart';
import '../data/models.dart';
import '../data/settings_repository.dart';
import '../l10n/app_localizations.dart';
import 'notification_content.dart';
import 'notification_policy.dart';
import 'notification_sink.dart';

/// Turns newly stored messages into notifications.
///
/// Messages that arrive close together on one topic (what piled up while the app was away
/// arrives in a burst) are announced as one notification once there are more than
/// [burstLimit] that deserve one, instead of a wall of them.
class NotificationService {
  NotificationService({
    required this.stored,
    required this.topicContext,
    required this.prefs,
    required this.sink,
    required this.now,
    required this.l10n,
    required this.viewing,
    this.burstWindow = const Duration(milliseconds: 300),
    this.burstLimit = 3,
  });

  /// Messages as they are newly stored.
  final Stream<IncomingMessage> stored;

  /// The topic's mute and minimum priority, or null if the user is not subscribed to it.
  final Future<TopicContext?> Function(int serverId, String topic) topicContext;
  final Future<NotificationPrefs> Function() prefs;
  final NotificationSink sink;
  final DateTime Function() now;
  final AppLocalizations Function() l10n;

  /// Whether the user is looking at this topic's thread right now (the window is focused on it).
  final bool Function(String host, String topic) viewing;

  final Duration burstWindow;
  final int burstLimit;

  StreamSubscription<IncomingMessage>? _subscription;
  final Map<String, List<IncomingMessage>> _pending = {};
  Timer? _timer;
  Future<void> _work = Future<void>.value();

  /// Starts listening.
  void start() {
    _subscription ??= stored.listen(_onStored);
  }

  void _onStored(IncomingMessage message) {
    if (message.silent) return;
    (_pending['${message.serverId}/${message.topic}'] ??= []).add(message);
    _timer ??= Timer(burstWindow, () => unawaited(flush()));
  }

  /// Announces what has been collected, now rather than when the burst window ends.
  Future<void> flush() {
    _timer?.cancel();
    _timer = null;
    final batches = _pending.values.map(List.of).toList();
    _pending.clear();
    _work = _work.then((_) async {
      for (final batch in batches) {
        try {
          await _announce(batch);
        } on Object catch (error, stack) {
          // A notification that cannot be shown must not stop the ones after it.
          developer.log(
            'could not show a notification: $error',
            name: 'notifications',
            error: error,
            stackTrace: stack,
          );
        }
      }
    });
    return _work;
  }

  Future<void> _announce(List<IncomingMessage> messages) async {
    final first = messages.first;
    final topic = await topicContext(first.serverId, first.topic);
    if (topic == null) return;
    final preferences = await prefs();
    final time = now();
    final isViewing = viewing(topic.host, first.topic);

    final due = <(IncomingMessage, NotificationLevel)>[];
    for (final message in messages) {
      final level = decideLevel(
        message: message,
        topic: topic,
        prefs: preferences,
        now: time,
        viewing: isViewing,
      );
      if (level != NotificationLevel.none) due.add((message, level));
    }
    if (due.isEmpty) return;

    final strings = l10n();
    final route = topicLocation(topic.host, first.topic);
    if (due.length > burstLimit) {
      // The most prominent level among them, so an urgent message is not hidden in a summary.
      final level = due
          .map((d) => d.$2)
          .reduce((a, b) => a.index >= b.index ? a : b);
      await _show(
        contentForBurst(
          l10n: strings,
          serverId: first.serverId,
          topic: first.topic,
          host: topic.host,
          route: route,
          count: due.length,
          level: level,
          sound: preferences.sound,
          newestId: due.last.$1.id,
        ),
      );
      return;
    }
    for (final (message, level) in due) {
      await _show(
        contentForMessage(
          l10n: strings,
          message: message,
          host: topic.host,
          route: route,
          level: level,
          sound: preferences.sound,
          showContent: preferences.showContent,
        ),
      );
    }
  }

  /// Shows one notification. One that cannot be shown must not stop the ones after it.
  Future<void> _show(ShownNotification notification) async {
    try {
      await sink.show(notification);
    } on Object catch (error, stack) {
      developer.log(
        'could not show a notification: $error',
        name: 'notifications',
        error: error,
        stackTrace: stack,
      );
    }
  }

  Future<void> stop() async {
    await _subscription?.cancel();
    _subscription = null;
    _timer?.cancel();
    _timer = null;
    await _work;
  }
}
