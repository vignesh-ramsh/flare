import 'package:app/data/models.dart';
import 'package:app/data/settings_repository.dart';
import 'package:app/notifications/notification_policy.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final now = DateTime(2025, 1, 15, 12);
  const topic = TopicContext(
    topicId: 1,
    host: 'example.com',
    muted: false,
    minPriority: 1,
  );

  IncomingMessage message({int priority = 3, bool silent = false}) =>
      IncomingMessage(
        serverId: 1,
        id: 'm',
        topic: 'alerts',
        time: 1,
        priority: priority,
        silent: silent,
      );

  NotificationLevel decide({
    int priority = 3,
    bool silent = false,
    TopicContext context = topic,
    NotificationPrefs prefs = const NotificationPrefs(),
    bool viewing = false,
  }) => decideLevel(
    message: message(priority: priority, silent: silent),
    topic: context,
    prefs: prefs,
    now: now,
    viewing: viewing,
  );

  group('by priority', () {
    test(
      '1 (min) is only listed',
      () => expect(decide(priority: 1), NotificationLevel.none),
    );
    test(
      '2 (low) is a notification without a sound',
      () => expect(decide(priority: 2), NotificationLevel.quiet),
    );
    test(
      '3 (default) is a notification',
      () => expect(decide(priority: 3), NotificationLevel.normal),
    );
    test(
      '4 (high) stays until dismissed',
      () => expect(decide(priority: 4), NotificationLevel.persistent),
    );
    test(
      '5 (urgent) stays until dismissed',
      () => expect(decide(priority: 5), NotificationLevel.persistent),
    );
  });

  group('stays in the list only', () {
    test('for history fetched on a first subscription, even if urgent', () {
      expect(decide(priority: 5, silent: true), NotificationLevel.none);
    });

    test('while notifications are off', () {
      expect(
        decide(prefs: const NotificationPrefs(enabled: false)),
        NotificationLevel.none,
      );
    });

    test('while paused, and not after the pause ends', () {
      final paused = NotificationPrefs(
        pausedUntil: now.add(const Duration(minutes: 5)),
      );
      expect(decide(prefs: paused), NotificationLevel.none);

      final over = NotificationPrefs(
        pausedUntil: now.subtract(const Duration(minutes: 5)),
      );
      expect(decide(prefs: over), NotificationLevel.normal);
    });

    test('on a muted topic, even if urgent', () {
      const muted = TopicContext(
        topicId: 1,
        host: 'example.com',
        muted: true,
        minPriority: 1,
      );
      expect(decide(priority: 5, context: muted), NotificationLevel.none);
    });

    test('below the topic\'s minimum priority, and not at or above it', () {
      const high = TopicContext(
        topicId: 1,
        host: 'example.com',
        muted: false,
        minPriority: 4,
      );
      expect(decide(priority: 3, context: high), NotificationLevel.none);
      expect(decide(priority: 4, context: high), NotificationLevel.persistent);
      expect(decide(priority: 5, context: high), NotificationLevel.persistent);
    });

    test('when the user is already looking at the thread', () {
      expect(decide(viewing: true), NotificationLevel.none);
      expect(decide(priority: 5, viewing: true), NotificationLevel.none);
    });
  });

  test('turning the sound off does not stop the notification', () {
    expect(
      decide(prefs: const NotificationPrefs(sound: false)),
      NotificationLevel.normal,
    );
  });

  test('the levels are ordered from least to most prominent', () {
    expect(NotificationLevel.values, [
      NotificationLevel.none,
      NotificationLevel.quiet,
      NotificationLevel.normal,
      NotificationLevel.persistent,
    ]);
  });
}
