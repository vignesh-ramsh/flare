import 'package:web_socket_channel/io.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

/// Opens a WebSocket from a desktop or phone app. [headers] carry the login, which
/// browsers cannot send. The app pings every [pingInterval] and closes a socket that
/// does not answer, so a connection that died silently (a laptop woke up on another
/// network) is noticed.
Future<WebSocketChannel> connectWebSocket(
  Uri uri,
  Map<String, String> headers, {
  required Duration pingInterval,
  required Duration connectTimeout,
}) async {
  final channel = IOWebSocketChannel.connect(
    uri,
    headers: headers,
    pingInterval: pingInterval,
    connectTimeout: connectTimeout,
  );
  await channel.ready;
  return channel;
}
