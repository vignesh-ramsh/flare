import 'dart:math';

import '../brand/brand.g.dart';
import 'server_url.dart';

/// A topic on a server, written as a link someone can send or show as a QR code:
///
///     <urlScheme>://subscribe/<host>/<topic>          (the server is reached over https)
///     <urlScheme>://subscribe/<host>/<topic>?scheme=http   (a server that only speaks http)
///
/// The scheme is the brand's. A plain web address of a topic, `https://host/topic`, is read as the
/// same thing, so a link copied from a script or a browser works too.
class SubscribeLink {
  const SubscribeLink({required this.server, required this.topic});

  /// The server, as the app stores it.
  final ServerAddress server;
  final String topic;

  /// Whether the server is reached over plain http.
  bool get plainHttp => server.url.startsWith('http://');

  /// Reads a subscribe link, or a web address of a topic. Returns null for anything else,
  /// including a link whose host or topic is not valid.
  ///
  /// With [webAddress] false only the app's own scheme is read: what the operating system hands
  /// the app is never taken for a web address.
  static SubscribeLink? parse(
    String input, {
    String scheme = Brand.urlScheme,
    bool webAddress = true,
  }) {
    final text = input.trim();
    if (text.isEmpty || text.contains(RegExp(r'\s'))) return null;
    final uri = Uri.tryParse(text);
    if (uri == null || !uri.hasScheme) return null;

    if (uri.scheme.toLowerCase() == scheme.toLowerCase()) {
      return _fromSchemeLink(uri);
    }
    if (webAddress && (uri.scheme == 'http' || uri.scheme == 'https')) {
      return _fromWebAddress(uri);
    }
    return null;
  }

  static SubscribeLink? _fromSchemeLink(Uri uri) {
    // `flare://subscribe/host/topic`: "subscribe" is the URI's host, the rest its path. Some
    // platforms hand over `flare:subscribe/host/topic` or `flare:///subscribe/...`; read all.
    final parts = <String>[
      if (uri.host.isNotEmpty) uri.host,
      ...uri.pathSegments.where((s) => s.isNotEmpty),
    ];
    if (parts.length != 3 || parts.first.toLowerCase() != 'subscribe') {
      return null;
    }
    final httpOnly = uri.queryParameters['scheme']?.toLowerCase() == 'http';
    final address = ServerAddress.parse(
      '${httpOnly ? 'http' : 'https'}://${parts[1]}',
    );
    return _make(address, parts[2]);
  }

  static SubscribeLink? _fromWebAddress(Uri uri) {
    if (uri.hasQuery || uri.hasFragment) return null;
    final segments = uri.pathSegments.where((s) => s.isNotEmpty).toList();
    if (segments.length != 1) return null;
    final address = ServerAddress.parse(
      uri.replace(path: '', query: null, fragment: null).toString(),
    );
    return _make(address, segments.single);
  }

  static SubscribeLink? _make(ServerAddress? address, String topic) {
    if (address == null || !isValidTopic(topic)) return null;
    return SubscribeLink(server: address, topic: topic);
  }

  /// The link to give to others.
  String format({String scheme = Brand.urlScheme}) =>
      '$scheme://subscribe/${server.host}/$topic${plainHttp ? '?scheme=http' : ''}';

  /// The route that opens the Subscribe form with this topic filled in.
  String get location =>
      '/subscribe/${Uri.encodeComponent(server.host)}/${Uri.encodeComponent(topic)}'
      '${plainHttp ? '?scheme=http' : ''}';

  /// The address of the topic on the web: what a script publishes to.
  String get webAddress => '${server.url}/$topic';

  @override
  bool operator ==(Object other) =>
      other is SubscribeLink &&
      other.server.url == server.url &&
      other.topic == topic;

  @override
  int get hashCode => Object.hash(server.url, topic);

  @override
  String toString() => format();
}

/// A name for a topic that nobody else will guess: lower-case letters and digits.
///
/// An open server lets anyone read and write a topic whose name they know, so the name is the
/// secret. Sixteen characters from 36 is about 82 bits.
String randomTopicName(Random random, {int length = 16}) {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789';
  return String.fromCharCodes([
    for (var i = 0; i < length; i++)
      alphabet.codeUnitAt(random.nextInt(alphabet.length)),
  ]);
}
