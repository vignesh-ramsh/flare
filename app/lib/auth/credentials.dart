import 'dart:async';
import 'dart:convert';
import 'dart:developer' as developer;

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// A saved sign-in on one server.
class Credentials {
  const Credentials({
    required this.token,
    required this.tokenId,
    required this.user,
  });

  /// The secret the app presents instead of a password.
  final String token;

  /// Names the token, so signing out can revoke it. Not a secret.
  final String tokenId;
  final String user;

  String toJson() =>
      jsonEncode({'token': token, 'tokenId': tokenId, 'user': user});

  static Credentials? fromJson(String text) {
    try {
      final map = jsonDecode(text);
      if (map is Map<String, dynamic> &&
          map['token'] is String &&
          map['tokenId'] is String &&
          map['user'] is String) {
        return Credentials(
          token: map['token'] as String,
          tokenId: map['tokenId'] as String,
          user: map['user'] as String,
        );
      }
    } on FormatException {
      // fall through: an unreadable entry is treated as no sign-in
    }
    return null;
  }
}

/// Where secrets are kept: the platform's keychain or keyring. Tokens never go in the
/// database, which is a plain file.
abstract interface class SecretStore {
  Future<Map<String, String>> readAll();
  Future<void> write(String key, String value);
  Future<void> delete(String key);
}

/// The platform's secure storage: Keychain on iOS and macOS, an encrypted store on
/// Android, the Credential Locker on Windows, the Secret Service (a keyring such as
/// GNOME Keyring or KWallet) on Linux.
class PlatformSecretStore implements SecretStore {
  PlatformSecretStore([FlutterSecureStorage? storage])
    : _storage =
          storage ??
          const FlutterSecureStorage(
            // The legacy keychain on macOS: the data-protection one needs an entitlement and
            // a provisioning profile, which a build for other Macs would not have.
            mOptions: MacOsOptions(usesDataProtectionKeychain: false),
          );

  final FlutterSecureStorage _storage;

  @override
  Future<Map<String, String>> readAll() => _storage.readAll();

  @override
  Future<void> write(String key, String value) =>
      _storage.write(key: key, value: value);

  @override
  Future<void> delete(String key) => _storage.delete(key: key);
}

/// Saved sign-ins by server address, and whether they survive closing the app.
class CredentialsState {
  const CredentialsState({this.byServer = const {}, this.persistent = true});

  /// By the server's base URL.
  final Map<String, Credentials> byServer;

  /// False when the secure storage could not be used (a Linux desktop with no keyring,
  /// for example). Sign-ins then last only until the app closes.
  final bool persistent;
}

const _prefix = 'credentials:';

/// Holds every saved sign-in. Keyed by the server's address, not its database ID, so a
/// reinstall that keeps the keychain (iOS does) still finds them.
class CredentialsController extends Notifier<CredentialsState> {
  @override
  CredentialsState build() => const CredentialsState();

  SecretStore get _store => ref.read(secretStoreProvider);

  /// A secure store that never answers (a keyring that is missing or stuck, which the
  /// platform plugin does not always report) must not hold the app up.
  Future<T> _guard<T>(Future<T> call) =>
      call.timeout(ref.read(secretStoreTimeoutProvider));

  /// Reads the saved sign-ins. Call once at launch, before connecting.
  Future<void> load() async {
    try {
      final all = await _guard(_store.readAll());
      final byServer = <String, Credentials>{};
      for (final entry in all.entries) {
        if (!entry.key.startsWith(_prefix)) continue;
        final credentials = Credentials.fromJson(entry.value);
        if (credentials != null) {
          byServer[entry.key.substring(_prefix.length)] = credentials;
        }
      }
      state = CredentialsState(byServer: byServer);
    } on Object catch (error) {
      developer.log('secure storage unavailable: $error', name: 'auth');
      state = const CredentialsState(persistent: false);
    }
  }

  Credentials? of(String serverUrl) => state.byServer[serverUrl];

  /// Remembers a sign-in. It is kept in memory even if the secure storage refuses it.
  Future<void> save(String serverUrl, Credentials credentials) async {
    var persistent = state.persistent;
    try {
      await _guard(_store.write('$_prefix$serverUrl', credentials.toJson()));
    } on Object catch (error) {
      developer.log('could not save the sign-in: $error', name: 'auth');
      persistent = false;
    }
    state = CredentialsState(
      byServer: {...state.byServer, serverUrl: credentials},
      persistent: persistent,
    );
  }

  /// Forgets a sign-in.
  Future<void> remove(String serverUrl) async {
    state = CredentialsState(
      byServer: {...state.byServer}..remove(serverUrl),
      persistent: state.persistent,
    );
    try {
      await _guard(_store.delete('$_prefix$serverUrl'));
    } on Object catch (error) {
      developer.log('could not delete the sign-in: $error', name: 'auth');
    }
  }
}

/// How long to wait for the secure storage before giving up on it.
final secretStoreTimeoutProvider = Provider<Duration>(
  (ref) => const Duration(seconds: 5),
);

/// The real secure storage. Tests replace it.
final secretStoreProvider = Provider<SecretStore>(
  (ref) => PlatformSecretStore(),
);

final credentialsProvider =
    NotifierProvider<CredentialsController, CredentialsState>(
      CredentialsController.new,
    );
