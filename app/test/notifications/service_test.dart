import 'dart:async';

import 'package:app/data/models.dart';
import 'package:app/data/settings_repository.dart';
import 'package:app/notifications/notification_policy.dart';
import 'package:app/notifications/notification_service.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fakes.dart';

void main() {
  final now = DateTime(2025, 1, 15, 12);
  late StreamController<IncomingMessage> stored;
  late RecordingSink sink;
  late NotificationService service;

  TopicContext? context = const TopicContext(
    topicId: 1,
    host: 'example.com',
    muted: false,
    minPriority: 1,
  );
  var prefs = const NotificationPrefs();
  var viewing = false;

  IncomingMessage message(
    int n, {
    String topic = 'alerts',
    int priority = 3,
    bool silent = false,
    String? title,
  }) => IncomingMessage(
    serverId: 1,
    id: 'm$n',
    topic: topic,
    time: 1,
    title: title ?? 'Message $n',
    body: 'text $n',
    priority: priority,
    silent: silent,
  );

  setUp(() {
    stored = StreamController<IncomingMessage>.broadcast();
    sink = RecordingSink();
    context = const TopicContext(
      topicId: 1,
      host: 'example.com',
      muted: false,
      minPriority: 1,
    );
    prefs = const NotificationPrefs();
    viewing = false;
    service = NotificationService(
      stored: stored.stream,
      topicContext: (serverId, topic) async =>
          topic == 'unknown' ? null : context,
      prefs: () async => prefs,
      sink: sink,
      now: () => now,
      l10n: english,
      viewing: (host, topic) => viewing,
      burstWindow: const Duration(milliseconds: 20),
    )..start();
  });

  tearDown(() async {
    await service.stop();
    await stored.close();
  });

  Future<void> receive(List<IncomingMessage> messages) async {
    messages.forEach(stored.add);
    await pumpEventQueue();
    await service.flush();
  }

  test('shows a notification for a message', () async {
    await receive([message(1, title: 'Disk almost full')]);

    expect(sink.shown, hasLength(1));
    expect(sink.shown.single.title, 'Disk almost full');
    expect(sink.shown.single.body, 'text 1');
    expect(sink.shown.single.route, '/topic/example.com/alerts');
    expect(sink.shown.single.level, NotificationLevel.normal);
  });

  test('waits a moment, so a burst can be grouped, when not flushed', () async {
    stored.add(message(1));
    await pumpEventQueue();
    expect(sink.shown, isEmpty);

    await Future<void>.delayed(const Duration(milliseconds: 80));
    expect(sink.shown, hasLength(1));
  });

  test('shows each of a few messages separately, in order', () async {
    await receive([message(1), message(2), message(3)]);
    expect(sink.shown.map((n) => n.title), [
      'Message 1',
      'Message 2',
      'Message 3',
    ]);
  });

  test('sums up a burst of more than three as one notification', () async {
    await receive([for (var i = 1; i <= 10; i++) message(i)]);

    expect(sink.shown, hasLength(1));
    expect(sink.shown.single.body, '10 new messages');
    expect(sink.shown.single.title, 'alerts');
  });

  test('keeps the most prominent level of a burst, so an urgent message is not buried', () async {
    await receive([
      for (var i = 1; i <= 6; i++) message(i),
      message(7, priority: 5),
    ]);

    expect(sink.shown, hasLength(1));
    expect(sink.shown.single.level, NotificationLevel.persistent);
  });

  test(
    'counts only the messages that deserve a notification when summing up',
    () async {
      await receive([
        for (var i = 1; i <= 4; i++) message(i, priority: 1), // min: list only
        message(5),
        message(6),
      ]);

      expect(sink.shown.map((n) => n.title), ['Message 5', 'Message 6']);
    },
  );

  test('groups per topic, not across topics', () async {
    await receive([
      for (var i = 1; i <= 5; i++) message(i, topic: 'alerts'),
      message(6, topic: 'backups'),
    ]);

    expect(sink.shown, hasLength(2));
    expect(sink.shown.map((n) => n.route).toSet(), {
      '/topic/example.com/alerts',
      '/topic/example.com/backups',
    });
  });

  group('shows nothing', () {
    test('for history', () async {
      await receive([
        message(1, silent: true),
        message(2, silent: true, priority: 5),
      ]);
      expect(sink.shown, isEmpty);
    });

    test('while notifications are off', () async {
      prefs = const NotificationPrefs(enabled: false);
      await receive([message(1)]);
      expect(sink.shown, isEmpty);
    });

    test('while paused', () async {
      prefs = NotificationPrefs(pausedUntil: now.add(const Duration(hours: 1)));
      await receive([message(1)]);
      expect(sink.shown, isEmpty);
    });

    test('for a muted topic', () async {
      context = const TopicContext(
        topicId: 1,
        host: 'example.com',
        muted: true,
        minPriority: 1,
      );
      await receive([message(1, priority: 5)]);
      expect(sink.shown, isEmpty);
    });

    test('below the topic\'s minimum priority', () async {
      context = const TopicContext(
        topicId: 1,
        host: 'example.com',
        muted: false,
        minPriority: 4,
      );
      await receive([message(1, priority: 3), message(2, priority: 4)]);
      expect(sink.shown.map((n) => n.title), ['High · Message 2']);
    });

    test('for the thread the user is looking at', () async {
      viewing = true;
      await receive([message(1)]);
      expect(sink.shown, isEmpty);

      viewing = false;
      await receive([message(2)]);
      expect(sink.shown, hasLength(1));
    });

    test('for a topic the user left', () async {
      await receive([message(1, topic: 'unknown')]);
      expect(sink.shown, isEmpty);
    });
  });

  test(
    'uses the choices the user has made at the moment it shows, not before',
    () async {
      stored.add(message(1));
      await pumpEventQueue();
      prefs = const NotificationPrefs(
        sound: false,
        showContent: false,
      ); // changed while it waited
      await service.flush();

      expect(sink.shown.single.sound, isFalse);
      expect(sink.shown.single.body, 'New message');
    },
  );

  test('carries on after a notification could not be shown', () async {
    sink.failOn = 0;
    await receive([message(1), message(2)]);

    expect(sink.shown.map((n) => n.title), ['Message 2']);
  });

  test('carries on with the next topic after one fails', () async {
    sink.failOn = 0;
    await receive([message(1, topic: 'alerts'), message(2, topic: 'backups')]);
    expect(sink.shown.map((n) => n.title), ['Message 2']);
  });

  test('stops listening when stopped', () async {
    await service.stop();
    stored.add(message(1));
    await pumpEventQueue();
    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(sink.shown, isEmpty);
  });

  test('starting twice listens once', () async {
    service.start();
    await receive([message(1)]);
    expect(sink.shown, hasLength(1));
  });
}
