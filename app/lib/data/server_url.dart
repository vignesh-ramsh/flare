/// A topic name as the server accepts it.
final RegExp _topicPattern = RegExp(r'^[A-Za-z0-9_-]{1,64}$');

/// A DNS name or an IPv4 address; IPv6 literals are checked by the URI parser.
final RegExp _hostPattern = RegExp(
  r'^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$',
);

bool isValidTopic(String name) => _topicPattern.hasMatch(name);

/// A server address the user typed, reduced to what the app stores.
class ServerAddress {
  const ServerAddress({required this.host, required this.url});

  /// `host` or `host:port` (default ports left out). Used in routes and as the server's key.
  final String host;

  /// The base URL without a trailing slash.
  final String url;

  /// Reads what a user typed. A missing scheme means https. Returns null for anything
  /// that is not a plain http(s) address: no host, user info, query or fragment.
  static ServerAddress? parse(String input) {
    var text = input.trim();
    if (text.isEmpty || text.contains(RegExp(r'\s'))) return null;
    if (!text.contains('://')) text = 'https://$text';

    final uri = Uri.tryParse(text);
    if (uri == null || !uri.hasAuthority || uri.host.isEmpty) return null;
    if (uri.scheme != 'http' && uri.scheme != 'https') return null;
    if (uri.userInfo.isNotEmpty || uri.hasQuery || uri.hasFragment) return null;
    final isIpv6 = uri.host.contains(':');
    if (!isIpv6 && !_hostPattern.hasMatch(uri.host)) return null;

    final defaultPort = uri.scheme == 'https' ? 443 : 80;
    final port = uri.hasPort && uri.port != defaultPort ? ':${uri.port}' : '';
    // Hosts are case-insensitive; IPv6 literals need their brackets back.
    final name = isIpv6 ? '[${uri.host}]' : uri.host.toLowerCase();
    final path = uri.path.endsWith('/')
        ? uri.path.substring(0, uri.path.length - 1)
        : uri.path;
    return ServerAddress(
      host: '$name$port',
      url: '${uri.scheme}://$name$port$path',
    );
  }
}
