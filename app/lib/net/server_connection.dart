import 'dart:async';
import 'dart:developer' as developer;

import 'package:web_socket_channel/web_socket_channel.dart';

import '../data/messages_repository.dart';
import '../data/models.dart';
import '../data/topics_repository.dart';
import 'backoff.dart';
import 'connector.dart';
import 'protocol.dart';
import 'server_status.dart';

/// Opens the socket. [connectWebSocket] is the real one; tests pass their own.
typedef SocketConnector = Future<WebSocketChannel> Function(
  Uri uri,
  Map<String, String> headers, {
  required Duration pingInterval,
  required Duration connectTimeout,
});

/// The saved login for a server, or null to connect anonymously.
typedef TokenSource = Future<String?> Function();

/// What a connection needs from the database.
abstract interface class ConnectionStore {
  /// Stores a message. Returns false if it was already there or is not wanted.
  Future<bool> storeMessage(IncomingMessage message);

  /// The ID of the newest message received on a topic, or null if none.
  Future<String?> cursorOf(int serverId, String topic);
}

class RepositoryConnectionStore implements ConnectionStore {
  RepositoryConnectionStore(this._topics, this._messages);

  final TopicsRepository _topics;
  final MessagesRepository _messages;

  @override
  Future<bool> storeMessage(IncomingMessage message) =>
      _messages.store(message);

  @override
  Future<String?> cursorOf(int serverId, String topic) =>
      _topics.cursorOf(serverId, topic);
}

enum _End { dropped, authRejected, noTopics }

/// One WebSocket to one server, following the topics it is given.
///
/// It connects when there is something to follow and keeps trying until [close]:
/// after a failure it waits longer each time (see [Backoff]), and starts from the
/// topic's cursor, so a reconnect catches up on what was missed without repeats.
/// A topic with no cursor yet starts from everything the server still has.
///
/// Messages are stored one at a time in the order they arrive, so the cursor never
/// moves past a message that was not stored.
class ServerConnection {
  ServerConnection({
    required this.serverId,
    required Uri url,
    required this._store,
    this._token,
    this._connector = connectWebSocket,
    Backoff? backoff,
    this.connectTimeout = const Duration(seconds: 15),
    this.pingInterval = const Duration(seconds: 20),
    this.stableAfter = const Duration(seconds: 10),
    this.relagDelay = const Duration(seconds: 1),
    this._now = DateTime.now,
  }) : _wsUri = websocketUri(url),
       _backoff = backoff ?? Backoff();

  final int serverId;
  final Uri _wsUri;
  final ConnectionStore _store;
  final TokenSource? _token;
  final SocketConnector _connector;
  final Backoff _backoff;
  final DateTime Function() _now;

  final Duration connectTimeout;
  final Duration pingInterval;

  /// A connection that lasted this long counts as healthy: the next retry is quick.
  final Duration stableAfter;

  /// How long to wait before following a topic again after the server dropped it for
  /// being too slow.
  final Duration relagDelay;

  final _statuses = StreamController<ServerStatus>.broadcast();
  ServerStatus _status = const ServerStatus();
  final Map<String, TopicLink> _links = {};

  Set<String> _topics = {};
  bool _closed = false;
  bool _authRejected = false;

  /// The login changed: drop the connection and open a new one at once, without waiting.
  bool _restart = false;
  Completer<void> _signal = Completer<void>();
  Future<void> _queue = Future<void>.value();
  _Session? _session;
  Future<void>? _loop;

  /// Changes to the status, as they happen. See [status] for the current one.
  Stream<ServerStatus> get statuses => _statuses.stream;

  ServerStatus get status => _status;

  /// The topics being followed.
  Set<String> get topics => Set.unmodifiable(_topics);

  /// Begins connecting, once there are topics.
  void start() => _loop ??= _run();

  /// Changes which topics to follow. Takes effect at once on an open connection.
  void setTopics(Set<String> topics) {
    if (_closed) return;
    final next = Set<String>.of(topics);
    if (_setEquals(next, _topics)) return;
    _topics = next;
    _links.removeWhere((topic, _) => !next.contains(topic));
    final session = _session;
    if (session != null) {
      if (next.isEmpty) {
        session.stop(_End.noTopics);
      } else {
        session.sync = session.sync
            .then((_) => _sync(session))
            .catchError(_log);
      }
    }
    _nudge();
    _emit();
  }

  /// Tries again now instead of waiting, and forgets that the server refused the login
  /// (call it after the login changed).
  void reconnectNow() {
    _authRejected = false;
    _nudge();
  }

  /// The saved sign-in changed (signed in, signed out, or a new token). Reconnects at once
  /// with the new login, on a connection that is open, waiting or refused.
  void loginChanged() {
    _authRejected = false;
    _restart = true;
    _session?.stop(_End.dropped);
    _nudge();
  }

  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _session?.stop(_End.noTopics);
    _nudge();
    await _loop;
    await _queue;
    await _statuses.close();
  }

  Future<void> _run() async {
    while (!_closed) {
      if (_topics.isEmpty) {
        _setState(LinkState.idle);
        await _signal.future;
        continue;
      }
      if (_authRejected) {
        _setState(LinkState.authFailed);
        await _signal.future;
        continue;
      }

      final started = _now();
      final end = await _connectAndFollow();
      if (_closed) break;
      // A new login: reconnect now, and do not count it as a failure.
      if (_restart) continue;

      switch (end) {
        case _End.noTopics:
          continue;
        case _End.authRejected:
          _authRejected = true;
          _backoff.reset();
          continue;
        case _End.dropped:
          if (_now().difference(started) >= stableAfter) _backoff.reset();
      }
      final delay = _backoff.next();
      _setState(
        LinkState.waiting,
        retryAt: _now().add(delay),
        failures: _backoff.failures,
      );
      await _sleep(delay);
    }
    _setState(LinkState.idle);
  }

  Future<_End> _connectAndFollow() async {
    _links.clear();
    _setState(LinkState.connecting, failures: _backoff.failures);

    final WebSocketChannel channel;
    try {
      final token = await _token?.call();
      _restart = false; // this attempt uses the current login
      channel = await _connector(
        _wsUri,
        {if (token != null) 'Authorization': 'Bearer $token'},
        pingInterval: pingInterval,
        connectTimeout: connectTimeout,
      );
    } on Object catch (error, stack) {
      _log(error, stack);
      return _refusedLogin(error) ? _End.authRejected : _End.dropped;
    }
    if (_closed) {
      _closeChannel(channel);
      return _End.noTopics;
    }

    final session = _Session(channel);
    _session = session;
    // The login changed while the connection was opening.
    if (_restart) session.stop(_End.dropped);
    final subscription = channel.stream.listen(
      (data) {
        if (data is String) _enqueue(session, data);
      },
      onError: (Object error, StackTrace stack) {
        _log(error, stack);
        session.stop(_End.dropped);
      },
      onDone: () => session.stop(_End.dropped),
      cancelOnError: true,
    );

    // A connection ends the run of failures for the user, though the backoff keeps its
    // count until the connection has proved stable, so a flapping server is not hammered.
    _setState(LinkState.connected);
    session.sync = session.sync.then((_) => _sync(session)).catchError(_log);

    final end = await session.ended.future;
    session.closed = true;
    _session = null;
    await subscription.cancel();
    _closeChannel(channel);
    await _queue; // let messages already received finish being stored
    // Close code 1008: the server ended the connection because the login stopped
    // being valid.
    if (channel.closeCode == 1008) return _End.authRejected;
    return _topics.isEmpty && end == _End.dropped ? _End.noTopics : end;
  }

  /// Makes the subscriptions on [session] match the wanted topics.
  Future<void> _sync(_Session session) async {
    if (session.closed) return;
    session.failed.removeWhere((topic) => !_topics.contains(topic));
    for (final topic in session.subscribed.difference(_topics).toList()) {
      session.subscribed.remove(topic);
      _send(session, unsubscribeFrame(topic));
    }
    for (final topic in _topics) {
      if (session.subscribed.contains(topic) ||
          session.failed.contains(topic)) {
        continue;
      }
      await _subscribe(session, topic);
      if (session.closed) return;
    }
  }

  Future<void> _subscribe(_Session session, String topic) async {
    final cursor = await _store.cursorOf(serverId, topic);
    if (session.closed || !_topics.contains(topic)) return;
    session.subscribed.add(topic);
    _send(session, subscribeFrame(topic, cursor ?? 'all'));
  }

  void _send(_Session session, String frame) {
    if (!session.closed) session.channel.sink.add(frame);
  }

  void _enqueue(_Session session, String text) {
    _queue = _queue.then((_) => _handle(session, text)).catchError(_log);
  }

  Future<void> _handle(_Session session, String text) async {
    switch (parseFrame(text)) {
      case null:
        developer.log('ignored a frame it could not read', name: 'connection');
      case final MessageFrame frame:
        if (_topics.contains(frame.topic)) {
          await _store.storeMessage(frame.toIncoming(serverId));
        }
      case final SubscribedFrame frame:
        if (_topics.contains(frame.topic)) {
          _links[frame.topic] = const TopicLink(live: true);
          _emit();
        }
      case final ErrorFrame frame:
        _onError(session, frame);
      case AuthenticatedFrame() || UnsubscribedFrame() || UnknownFrame():
        break;
    }
  }

  void _onError(_Session session, ErrorFrame error) {
    final topic = error.topic;
    developer.log(
      'server error ${error.code}: ${error.message}',
      name: 'connection',
    );

    if (error.code == ErrorCode.invalidCredentials) {
      session.stop(_End.authRejected);
      return;
    }
    if (topic == null || !_topics.contains(topic)) return;

    switch (error.code) {
      case ErrorCode.alreadySubscribed:
        session.subscribed.add(topic);
      case ErrorCode.lagged:
        // Dropped for being too slow: follow again from the cursor, which is current
        // because every earlier message has been stored.
        session.subscribed.remove(topic);
        _links[topic] = const TopicLink();
        _emit();
        Timer(relagDelay, () {
          if (session.closed) return;
          session.sync = session.sync
              .then((_) => _sync(session))
              .catchError(_log);
        });
      default:
        session.subscribed.remove(topic);
        session.failed.add(topic);
        _links[topic] = TopicLink(errorCode: error.code);
        _emit();
    }
  }

  /// Whether the server answered the connection request with 401: the login in the
  /// request was not valid.
  bool _refusedLogin(Object error) => RegExp(r'\b401\b').hasMatch('$error');

  void _closeChannel(WebSocketChannel channel) {
    unawaited(
      channel.sink
          .close(1000)
          .timeout(const Duration(seconds: 2), onTimeout: () {})
          .catchError((Object _) {}),
    );
  }

  void _setState(LinkState state, {DateTime? retryAt, int failures = 0}) {
    _status = ServerStatus(
      state: state,
      retryAt: retryAt,
      failures: failures,
      topics: Map.of(_links),
    );
    if (!_statuses.isClosed) _statuses.add(_status);
  }

  void _emit() => _setState(
    _status.state,
    retryAt: _status.retryAt,
    failures: _status.failures,
  );

  void _nudge() {
    final signal = _signal;
    _signal = Completer<void>();
    signal.complete();
  }

  Future<void> _sleep(Duration duration) {
    final timer = Timer(duration, _nudge);
    return _signal.future.whenComplete(timer.cancel);
  }

  void _log(Object error, [StackTrace? stack]) {
    developer.log(
      '$error',
      name: 'connection',
      error: error,
      stackTrace: stack,
    );
  }
}

class _Session {
  _Session(this.channel);

  final WebSocketChannel channel;
  final Completer<_End> ended = Completer<_End>();

  /// Topics sent a `subscribe` on this connection.
  final Set<String> subscribed = {};

  /// Topics the server refused on this connection; not tried again until it reconnects.
  final Set<String> failed = {};

  Future<void> sync = Future<void>.value();
  bool closed = false;

  void stop(_End reason) {
    if (!ended.isCompleted) ended.complete(reason);
  }
}

bool _setEquals(Set<String> a, Set<String> b) =>
    a.length == b.length && a.containsAll(b);

/// `https://host/prefix` becomes `wss://host/prefix/v1/ws`.
Uri websocketUri(Uri base) {
  final scheme = base.scheme == 'https' ? 'wss' : 'ws';
  final prefix = base.path.endsWith('/')
      ? base.path.substring(0, base.path.length - 1)
      : base.path;
  return base.replace(scheme: scheme, path: '$prefix/v1/ws');
}
