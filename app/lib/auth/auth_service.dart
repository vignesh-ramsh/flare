import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../brand/brand.g.dart';
import '../core/db/database.dart';
import '../net/api_client.dart';
import '../net/providers.dart';
import 'credentials.dart';

/// Signing in and out of a server. Failures are [ApiException]s.
class AuthService {
  AuthService(this._ref);

  final Ref _ref;

  /// Signs in with a password. The password is used once and not kept: what is saved is
  /// the token the server returns. The connection to the server restarts with it.
  Future<Credentials> signIn(
    ServerRow server, {
    required String user,
    required String password,
  }) async {
    final result = await _ref
        .read(apiProvider)
        .login(
          Uri.parse(server.url),
          user: user,
          password: password,
          // Shown to the user in their list of signed-in devices.
          label: '${Brand.name} (${defaultTargetPlatform.name})',
        );
    final credentials = Credentials(
      token: result.token,
      tokenId: result.tokenId,
      user: result.user,
    );
    await _ref.read(credentialsProvider.notifier).save(server.url, credentials);
    _ref.read(connectionManagerProvider).loginChanged(server.url);
    return credentials;
  }

  /// Signs out on this device and asks the server to revoke the token. Returns whether the
  /// server did: when it could not be reached the token stays valid until revoked from
  /// another device, though this one no longer has it.
  Future<bool> signOut(ServerRow server) async {
    final credentials = _ref.read(credentialsProvider.notifier).of(server.url);
    if (credentials == null) return true;

    var revoked = true;
    try {
      await _ref
          .read(apiProvider)
          .revokeToken(
            Uri.parse(server.url),
            token: credentials.token,
            tokenId: credentials.tokenId,
          );
    } on ApiException {
      revoked = false;
    }
    await _ref.read(credentialsProvider.notifier).remove(server.url);
    _ref.read(connectionManagerProvider).loginChanged(server.url);
    return revoked;
  }
}

final authServiceProvider = Provider<AuthService>(AuthService.new);
