import 'dart:convert';

import 'package:app/net/protocol.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('parseFrame', () {
    test('reads a message in the shape the server sends', () {
      final frame = parseFrame(
        jsonEncode({
          'id': '01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c21',
          'time': 1700000000,
          'expires': 1700086400,
          'event': 'message',
          'topic': 'alerts',
          'title': 'Disk',
          'message': 'full',
          'priority': 4,
          'tags': ['db', 'prod'],
          'click': 'https://example.com',
        }),
      ) as MessageFrame;

      expect(frame.id, '01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c21');
      expect(frame.topic, 'alerts');
      expect(frame.time, 1700000000);
      expect(frame.title, 'Disk');
      expect(frame.body, 'full');
      expect(frame.priority, 4);
      expect(frame.tags, ['db', 'prod']);
      expect(frame.click, 'https://example.com');
    });

    test('fills in what the server leaves out', () {
      final frame = parseFrame(
        '{"id":"a","time":1,"event":"message","topic":"t"}',
      ) as MessageFrame;
      expect(
        (frame.title, frame.body, frame.priority, frame.click),
        (null, '', 3, null),
      );
      expect(frame.tags, isEmpty);
    });

    test('keeps only string tags, and a priority between 1 and 5', () {
      final frame = parseFrame(
        '{"id":"a","time":1,"event":"message","topic":"t","tags":["x",1,null],"priority":9}',
      ) as MessageFrame;
      expect(frame.tags, ['x']);
      expect(frame.priority, 3);
    });

    test('turns a message into one to store, for a server', () {
      final frame = parseFrame(
        '{"id":"a","time":5,"event":"message","topic":"t","title":"T","message":"m","priority":2}',
      ) as MessageFrame;
      final incoming = frame.toIncoming(7);
      expect(
        (incoming.serverId, incoming.id, incoming.topic, incoming.time),
        (7, 'a', 't', 5),
      );
      expect((incoming.title, incoming.body, incoming.priority), ('T', 'm', 2));
    });

    test('refuses a message missing its id, topic or time', () {
      for (final text in [
        '{"time":1,"event":"message","topic":"t"}',
        '{"id":"","time":1,"event":"message","topic":"t"}',
        '{"id":"a","time":1,"event":"message"}',
        '{"id":"a","event":"message","topic":"t"}',
        '{"id":"a","time":"1","event":"message","topic":"t"}',
        '{"id":5,"time":1,"event":"message","topic":"t"}',
      ]) {
        expect(parseFrame(text), isNull, reason: text);
      }
    });

    test('reads the control events', () {
      expect(
        (parseFrame(
          '{"event":"subscribed","topic":"t"}',
        ) as SubscribedFrame).topic,
        't',
      );
      expect(
        (parseFrame(
          '{"event":"unsubscribed","topic":"t"}',
        ) as UnsubscribedFrame).topic,
        't',
      );
      expect(
        (parseFrame(
          '{"event":"authenticated","user":"alice"}',
        ) as AuthenticatedFrame).user,
        'alice',
      );
    });

    test('reads an error with and without a topic', () {
      final withTopic = parseFrame(
        '{"event":"error","code":40301,"error":"forbidden","topic":"t"}',
      ) as ErrorFrame;
      expect(
        (withTopic.code, withTopic.message, withTopic.topic),
        (40301, 'forbidden', 't'),
      );

      final without = parseFrame(
        '{"event":"error","code":40102,"error":"bad token"}',
      ) as ErrorFrame;
      expect(without.topic, isNull);
    });

    test('keeps an event it does not know, so servers can add events', () {
      expect(
        (parseFrame(
          '{"event":"message_deleted","id":"a"}',
        ) as UnknownFrame).event,
        'message_deleted',
      );
    });

    test('returns null for anything that is not an event', () {
      for (final text in [
        '',
        'not json',
        '[]',
        '"x"',
        '{}',
        '{"event":3}',
        '{"event":"error"}',
        '{"event":"subscribed"}',
      ]) {
        expect(parseFrame(text), isNull, reason: text);
      }
    });
  });

  test('writes subscribe and unsubscribe frames', () {
    expect(jsonDecode(subscribeFrame('alerts', 'all')), {
      'op': 'subscribe',
      'topic': 'alerts',
      'since': 'all',
    });
    expect(jsonDecode(subscribeFrame('alerts', 'abc')), {
      'op': 'subscribe',
      'topic': 'alerts',
      'since': 'abc',
    });
    expect(jsonDecode(unsubscribeFrame('alerts')), {
      'op': 'unsubscribe',
      'topic': 'alerts',
    });
  });
}
