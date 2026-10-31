import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

/// Why a request to a server failed, in the terms a user can act on.
enum ApiFailure {
  /// No answer: the address is wrong, the server is down, or there is no network.
  unreachable,

  /// The server did not answer in time.
  timeout,

  /// The server's certificate was not accepted.
  certificate,

  /// The address answered, but not like a server this app can use.
  notSupported,

  /// The user name or password (or token) was wrong.
  badCredentials,

  /// Too many failed sign-ins from this address; try later.
  throttled,

  /// The server answered with an error of its own.
  server,
}

class ApiException implements Exception {
  const ApiException(this.failure, {this.code, this.message});

  final ApiFailure failure;

  /// The server's error code (api/openapi.yaml, `Error`), if it sent one.
  final int? code;
  final String? message;

  @override
  String toString() => 'ApiException($failure, code: $code, $message)';
}

/// What a server says about itself (`GET /v1/config`).
class ServerInfo {
  const ServerInfo({
    required this.name,
    required this.apiVersion,
    required this.defaultAccess,
    required this.features,
  });

  final String name;
  final int apiVersion;

  /// What a visitor who is not signed in may do: `read-write`, `read-only`, `write-only`
  /// or `deny-all`.
  final String defaultAccess;
  final List<String> features;

  /// Whether reading needs a sign-in. Individual topics may still differ.
  bool get needsSignIn =>
      defaultAccess == 'deny-all' || defaultAccess == 'write-only';
}

/// A fresh access token, shown by the server only once.
class LoginResult {
  const LoginResult({
    required this.token,
    required this.tokenId,
    required this.user,
    required this.role,
  });

  final String token;

  /// Names the token for revoking it; not a secret.
  final String tokenId;
  final String user;
  final String role;
}

/// `https://host/prefix` and `/v1/config` make `https://host/prefix/v1/config`.
Uri apiUri(Uri base, String path) {
  final prefix = base.path.endsWith('/')
      ? base.path.substring(0, base.path.length - 1)
      : base.path;
  return base.replace(path: '$prefix$path');
}

/// The REST calls the app makes. The live connection is in `server_connection.dart`.
class ServerApi {
  ServerApi({http.Client? client, this.timeout = const Duration(seconds: 15)})
    : _client = client ?? http.Client();

  final http.Client _client;
  final Duration timeout;

  /// Reads a server's public settings. Also the check that an address is a server this app can use.
  Future<ServerInfo> config(Uri base) async {
    final response = await _send(
      () => _client.get(apiUri(base, '/v1/config'), headers: _json),
    );
    final body = _decode(response.body);
    if (response.statusCode != 200 ||
        body is! Map<String, dynamic> ||
        body['api_version'] is! int ||
        body['name'] is! String) {
      throw const ApiException(ApiFailure.notSupported);
    }
    final auth = body['auth'];
    final access = auth is Map<String, dynamic> ? auth['default_access'] : null;
    final features = body['features'];
    return ServerInfo(
      name: body['name'] as String,
      apiVersion: body['api_version'] as int,
      defaultAccess: access is String ? access : 'read-write',
      features: features is List
          ? [
              for (final f in features)
                if (f is String) f,
            ]
          : const [],
    );
  }

  /// Signs in with a password and returns a token to keep instead of it.
  /// [label] names the token in the account's list of devices.
  Future<LoginResult> login(
    Uri base, {
    required String user,
    required String password,
    String? label,
  }) async {
    final basic = base64.encode(utf8.encode('$user:$password'));
    final response = await _send(
      () => _client.post(
        apiUri(base, '/v1/auth/login'),
        headers: {
          ..._json,
          'Authorization': 'Basic $basic',
          'Content-Type': 'application/json',
        },
        body: jsonEncode({'label': ?label}),
      ),
    );
    final body = _decode(response.body);
    if (response.statusCode == 200 &&
        body is Map<String, dynamic> &&
        body['token'] is String &&
        body['id'] is String &&
        body['user'] is String) {
      return LoginResult(
        token: body['token'] as String,
        tokenId: body['id'] as String,
        user: body['user'] as String,
        role: body['role'] is String ? body['role'] as String : 'user',
      );
    }
    throw _errorFrom(response, body);
  }

  /// Revokes a token, which signs that device out. A token that is already gone counts as done.
  Future<void> revokeToken(
    Uri base, {
    required String token,
    required String tokenId,
  }) async {
    final response = await _send(
      () => _client.delete(
        apiUri(base, '/v1/account/tokens/${Uri.encodeComponent(tokenId)}'),
        headers: {..._json, 'Authorization': 'Bearer $token'},
      ),
    );
    if (response.statusCode == 204 ||
        response.statusCode == 404 ||
        response.statusCode == 401) {
      return;
    }
    throw _errorFrom(response, _decode(response.body));
  }

  void close() => _client.close();

  static const _json = {'Accept': 'application/json'};

  Future<http.Response> _send(Future<http.Response> Function() request) async {
    try {
      return await request().timeout(timeout);
    } on TimeoutException {
      throw const ApiException(ApiFailure.timeout);
    } on http.ClientException catch (error) {
      final text = '$error';
      if (text.contains('CERTIFICATE') || text.contains('HandshakeException')) {
        throw ApiException(ApiFailure.certificate, message: text);
      }
      throw ApiException(ApiFailure.unreachable, message: text);
    }
  }

  Object? _decode(String body) {
    try {
      return jsonDecode(body);
    } on FormatException {
      return null;
    }
  }

  ApiException _errorFrom(http.Response response, Object? body) {
    final code = body is Map<String, dynamic> && body['code'] is int
        ? body['code'] as int
        : null;
    final message = body is Map<String, dynamic> && body['error'] is String
        ? body['error'] as String
        : null;
    final failure = switch (response.statusCode) {
      401 => ApiFailure.badCredentials,
      429 => ApiFailure.throttled,
      404 => ApiFailure.notSupported,
      _ => ApiFailure.server,
    };
    return ApiException(failure, code: code, message: message);
  }
}
