import 'dart:async';

import 'package:app/auth/credentials.dart';

/// A secure store that lives in memory, for tests.
class MemorySecretStore implements SecretStore {
  final Map<String, String> values = {};

  @override
  Future<Map<String, String>> readAll() async => Map.of(values);

  @override
  Future<void> write(String key, String value) async => values[key] = value;

  @override
  Future<void> delete(String key) async => values.remove(key);
}

/// A secure store that refuses everything, as a Linux desktop with no keyring does.
class BrokenSecretStore implements SecretStore {
  @override
  Future<Map<String, String>> readAll() =>
      Future.error(StateError('no keyring'));

  @override
  Future<void> write(String key, String value) =>
      Future.error(StateError('no keyring'));

  @override
  Future<void> delete(String key) => Future.error(StateError('no keyring'));
}

/// A secure store that never answers, as a stuck keyring can.
class HangingSecretStore implements SecretStore {
  @override
  Future<Map<String, String>> readAll() =>
      Completer<Map<String, String>>().future;

  @override
  Future<void> write(String key, String value) => Completer<void>().future;

  @override
  Future<void> delete(String key) => Completer<void>().future;
}
