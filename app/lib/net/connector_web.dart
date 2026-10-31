import 'package:web_socket_channel/web_socket_channel.dart';

/// Opens a WebSocket from a browser. Browsers cannot set request headers, so a login
/// has to be sent as an `auth` frame instead (not built yet), and the browser
/// pings for us.
Future<WebSocketChannel> connectWebSocket(
  Uri uri,
  Map<String, String> headers, {
  required Duration pingInterval,
  required Duration connectTimeout,
}) async {
  final channel = WebSocketChannel.connect(uri);
  await channel.ready.timeout(connectTimeout);
  return channel;
}
