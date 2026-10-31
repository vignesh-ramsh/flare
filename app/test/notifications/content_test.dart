import 'package:app/data/models.dart';
import 'package:app/notifications/notification_content.dart';
import 'package:app/notifications/notification_policy.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fakes.dart';

void main() {
  final l10n = english();

  IncomingMessage message({
    String? title,
    String body = 'db-01 /var is at 92%',
    int priority = 3,
  }) => IncomingMessage(
    serverId: 7,
    id: 'm1',
    topic: 'alerts-db',
    time: 1,
    title: title,
    body: body,
    priority: priority,
  );

  ShownNotification content(
    IncomingMessage m, {
    NotificationLevel level = NotificationLevel.normal,
    bool sound = true,
    bool showContent = true,
  }) => contentForMessage(
    l10n: l10n,
    message: m,
    host: 'example.com',
    route: '/topic/example.com/alerts-db',
    level: level,
    sound: sound,
    showContent: showContent,
  );

  group('one message', () {
    test('shows its title and text, and where it came from', () {
      final n = content(message(title: 'Disk almost full'));
      expect(n.title, 'Disk almost full');
      expect(n.body, 'db-01 /var is at 92%');
      expect(n.subtitle, 'alerts-db on example.com');
      expect(n.route, '/topic/example.com/alerts-db');
    });

    test('is titled by its topic when it has no title', () {
      final n = content(message());
      expect(n.title, 'alerts-db');
      expect(n.subtitle, 'example.com');
      expect(n.body, 'db-01 /var is at 92%');
    });

    test('names a priority that is not the default, with a word', () {
      expect(
        content(message(title: 'Disk', priority: 5)).title,
        'Urgent · Disk',
      );
      expect(content(message(title: 'Disk', priority: 4)).title, 'High · Disk');
      expect(content(message(title: 'Disk', priority: 2)).title, 'Low · Disk');
      expect(content(message(title: 'Disk', priority: 3)).title, 'Disk');
    });

    test('keeps the text out when the user asked it not to show', () {
      final n = content(
        message(title: 'Secret title', body: 'secret body'),
        showContent: false,
      );
      expect(n.title, 'alerts-db');
      expect(n.body, 'New message');
      expect(n.subtitle, 'alerts-db on example.com');
      expect('${n.title} ${n.body} ${n.subtitle}', isNot(contains('secret')));
    });

    test(
      'has a sound only if one is allowed and it is not a quiet notification',
      () {
        expect(content(message()).sound, isTrue);
        expect(content(message(), sound: false).sound, isFalse);
        expect(
          content(message(), level: NotificationLevel.quiet).sound,
          isFalse,
        );
        expect(
          content(message(), level: NotificationLevel.persistent).sound,
          isTrue,
        );
      },
    );

    test('has the same ID each time, and another for another message', () {
      expect(content(message()).id, content(message()).id);
      final other = IncomingMessage(
        serverId: 7,
        id: 'm2',
        topic: 'alerts-db',
        time: 1,
      );
      expect(content(other).id, isNot(content(message()).id));
    });
  });

  group('a burst', () {
    ShownNotification burst({
      int count = 12,
      NotificationLevel level = NotificationLevel.normal,
      bool sound = true,
    }) => contentForBurst(
      l10n: l10n,
      serverId: 7,
      topic: 'alerts-db',
      host: 'example.com',
      route: '/topic/example.com/alerts-db',
      count: count,
      level: level,
      sound: sound,
      newestId: 'm12',
    );

    test('says how many, and where', () {
      final n = burst();
      expect(
        (n.title, n.body, n.subtitle),
        ('alerts-db', '12 new messages', 'alerts-db on example.com'),
      );
      expect(n.route, '/topic/example.com/alerts-db');
    });

    test('is a different notification from the single messages in it', () {
      expect(burst().id, isNot(content(message()).id));
    });
  });

  group('notificationId', () {
    test('is stable, positive, and fits 31 bits', () {
      for (final text in [
        '',
        'a',
        '1/01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c21',
        'é' * 100,
      ]) {
        final id = notificationId(text);
        expect(id, notificationId(text));
        expect(id, inInclusiveRange(0, 0x7FFFFFFF));
      }
    });

    test('differs for different text', () {
      final ids = {
        for (var i = 0; i < 1000; i++) notificationId('1/message-$i'),
      };
      expect(ids.length, greaterThan(990));
    });
  });
}
