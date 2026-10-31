import '../data/models.dart';
import '../data/settings_repository.dart';

/// How prominently a message announces itself.
enum NotificationLevel {
  /// Not at all: it is only in the list.
  none,

  /// A notification with no sound.
  quiet,

  /// A notification, with a sound if the user allows one.
  normal,

  /// A notification that stays until the user dismisses it, with a sound if allowed.
  persistent,
}

/// Decides how a newly stored message should be announced. Pure, so every rule is a test.
///
/// In order:
/// 1. history fetched when a topic was first subscribed to, the user's own messages, and everything
///    while notifications are off or paused, or the topic is muted, stays in the list only;
/// 2. a message below the topic's minimum priority does too, and so does priority 1 (min);
/// 3. a message the user is already looking at (the window is focused on that thread) needs no
///    announcement;
/// 4. priority 2 is a notification without a sound, 3 is a normal one, 4 and 5 stay until
///    dismissed.
NotificationLevel decideLevel({
  required IncomingMessage message,
  required TopicContext topic,
  required NotificationPrefs prefs,
  required DateTime now,
  required bool viewing,
}) {
  // History, and the user's own message: nothing to announce about what they just sent.
  if (message.silent || message.mine) return NotificationLevel.none;
  if (!prefs.enabled || prefs.pausedAt(now)) return NotificationLevel.none;
  if (topic.muted) return NotificationLevel.none;
  if (message.priority < topic.minPriority) return NotificationLevel.none;
  if (message.priority <= 1) return NotificationLevel.none;
  if (viewing) return NotificationLevel.none;
  return switch (message.priority) {
    2 => NotificationLevel.quiet,
    3 => NotificationLevel.normal,
    _ => NotificationLevel.persistent,
  };
}
