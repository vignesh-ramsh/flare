/// Where a server's connection is.
enum LinkState {
  /// Nothing to follow on this server, so no connection is held.
  idle,

  /// Opening the connection.
  connecting,

  /// Connected; topics are being followed.
  connected,

  /// The last attempt failed or the connection dropped; trying again at `retryAt`.
  waiting,

  /// The server refused the saved login. Nothing is retried until the login changes.
  authFailed,
}

/// How one topic is doing on a connected server.
class TopicLink {
  const TopicLink({this.live = false, this.errorCode});

  /// The server confirmed the subscription.
  final bool live;

  /// The server's error code if it refused this topic (for example 40301, forbidden).
  final int? errorCode;
}

class ServerStatus {
  const ServerStatus({
    this.state = LinkState.idle,
    this.retryAt,
    this.failures = 0,
    this.topics = const {},
  });

  final LinkState state;

  /// When the next attempt starts, while [state] is [LinkState.waiting].
  final DateTime? retryAt;

  /// Attempts in a row that failed.
  final int failures;

  /// By topic name. Only topics the server has answered for appear.
  final Map<String, TopicLink> topics;

  /// Whether the user should be told something is wrong with this server.
  bool get needsAttention =>
      state == LinkState.waiting || state == LinkState.authFailed;
}
