import 'dart:async';
import 'dart:convert';
import 'dart:io';

/// A stand-in for a Flare server that speaks the `/v1/ws` protocol, so the connection
/// code can be tested for reconnecting, catching up and errors without a real server.
///
/// Like the real one, it answers `subscribe` with `subscribed` first and then replays
/// the backlog after `since`, and it only sends live messages to sockets that follow
/// the topic.
class FakeFlareServer {
  HttpServer? _http;
  final List<FakeSocket> sockets = [];

  /// Every frame any client sent, in order.
  final List<Map<String, dynamic>> received = [];

  /// The request headers of every upgrade request.
  final List<Map<String, String>> requestHeaders = [];

  /// Messages the server holds, by topic, oldest first.
  final Map<String, List<Map<String, dynamic>>> backlog = {};

  /// Answer the upgrade request with this HTTP status instead of upgrading.
  int? refuseUpgradeWith;

  /// Answer `subscribe` for these topics with an error frame carrying this code.
  final Map<String, int> errorOnSubscribe = {};

  /// How many upgrade requests were made, accepted or not.
  int attempts = 0;

  int _port = 0;

  /// The port it listens on, kept after [stop] so a restart can reuse it.
  int get port => _port;
  Uri get url => Uri.parse('http://127.0.0.1:$port');

  Future<void> start({int port = 0}) async {
    _http = await HttpServer.bind(InternetAddress.loopbackIPv4, port);
    _port = _http!.port;
    _http!.listen(_handle);
  }

  Future<void> stop() async {
    final http = _http;
    _http = null;
    for (final socket in sockets.toList()) {
      socket.socket.close(WebSocketStatus.goingAway).ignore();
    }
    await http?.close(force: true);
  }

  Future<void> _handle(HttpRequest request) async {
    attempts++;
    requestHeaders.add({
      for (final name in const ['authorization', 'upgrade'])
        if (request.headers.value(name) != null)
          name: request.headers.value(name)!,
    });
    final status = refuseUpgradeWith;
    if (status != null) {
      request.response.statusCode = status;
      await request.response.close();
      return;
    }
    final socket = FakeSocket(await WebSocketTransformer.upgrade(request));
    sockets.add(socket);
    socket.socket.listen(
      (data) => _onFrame(socket, data as String),
      onDone: () => sockets.remove(socket),
      onError: (Object _) => sockets.remove(socket),
    );
  }

  void _onFrame(FakeSocket socket, String data) {
    final frame = jsonDecode(data) as Map<String, dynamic>;
    received.add(frame);
    final topic = frame['topic'] as String?;
    switch (frame['op']) {
      case 'subscribe':
        final code = errorOnSubscribe[topic];
        if (code != null) {
          socket.send({
            'event': 'error',
            'code': code,
            'error': 'refused',
            'topic': topic,
          });
          return;
        }
        socket.topics.add(topic!);
        socket.send({'event': 'subscribed', 'topic': topic});
        final since = frame['since'] as String?;
        for (final m in backlog[topic] ?? const <Map<String, dynamic>>[]) {
          if (since == 'all' ||
              (since != null && (m['id'] as String).compareTo(since) > 0)) {
            socket.send(m);
          }
        }
      case 'unsubscribe':
        socket.topics.remove(topic);
        socket.send({'event': 'unsubscribed', 'topic': topic});
    }
  }

  /// Adds a message to the backlog and sends it to the sockets following its topic.
  void publish(Map<String, dynamic> message) {
    final topic = message['topic'] as String;
    backlog.putIfAbsent(topic, () => []).add(message);
    for (final socket in sockets) {
      if (socket.topics.contains(topic)) socket.send(message);
    }
  }

  /// Adds a message to the backlog without sending it: it arrives while no one is connected.
  void keep(Map<String, dynamic> message) =>
      backlog.putIfAbsent(message['topic'] as String, () => []).add(message);

  /// Sends a frame to every connected socket.
  void sendToAll(Map<String, dynamic> frame) {
    for (final socket in sockets) {
      socket.send(frame);
    }
  }

  void sendRawToAll(String text) {
    for (final socket in sockets) {
      socket.socket.add(text);
    }
  }

  /// Closes every connection, as a restarting server does.
  void dropConnections([int code = WebSocketStatus.goingAway]) {
    for (final socket in sockets.toList()) {
      socket.socket.close(code).ignore();
    }
  }

  /// The `subscribe` frames received for [topic], in order.
  List<Map<String, dynamic>> subscribesFor(String topic) => [
    for (final f in received)
      if (f['op'] == 'subscribe' && f['topic'] == topic) f,
  ];
}

class FakeSocket {
  FakeSocket(this.socket);

  final WebSocket socket;
  final Set<String> topics = {};

  void send(Map<String, dynamic> frame) {
    try {
      socket.add(jsonEncode(frame));
    } on StateError {
      // The client's frame crossed our close; there is no one to answer.
    }
  }
}

/// A message as the server sends it.
Map<String, dynamic> wireMessage(
  String topic,
  String id, {
  String? title,
  String? message,
  int? priority,
  List<String>? tags,
  int time = 1700000000,
}) => {
  'id': id,
  'time': time,
  'event': 'message',
  'topic': topic,
  'title': ?title,
  'message': ?message,
  'priority': ?priority,
  'tags': ?tags,
};

/// Waits until [condition] holds, polling, or fails the test after [timeout].
Future<void> eventually(
  FutureOr<bool> Function() condition, {
  Duration timeout = const Duration(seconds: 5),
  String? reason,
}) async {
  final deadline = DateTime.now().add(timeout);
  while (true) {
    if (await condition()) return;
    if (DateTime.now().isAfter(deadline)) {
      throw TimeoutException(
        'Condition not met: ${reason ?? 'eventually'}',
        timeout,
      );
    }
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
}
