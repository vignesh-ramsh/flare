import 'package:app/auth/credentials.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'memory_store.dart';

void main() {
  const alice = Credentials(token: 'flr_a', tokenId: 'tok_a', user: 'alice');
  const bob = Credentials(token: 'flr_b', tokenId: 'tok_b', user: 'bob');

  ProviderContainer containerWith(SecretStore store) {
    final container = ProviderContainer(
      overrides: [secretStoreProvider.overrideWithValue(store)],
    );
    addTearDown(container.dispose);
    return container;
  }

  test('starts with no sign-ins and says they are kept', () {
    final state = containerWith(MemorySecretStore()).read(credentialsProvider);
    expect(state.byServer, isEmpty);
    expect(state.persistent, isTrue);
  });

  test('loads what an earlier run saved, by server address', () async {
    final store = MemorySecretStore()
      ..values['credentials:https://example.com'] = alice.toJson()
      ..values['credentials:http://192.168.1.5:8080'] = bob.toJson();
    final container = containerWith(store);
    await container.read(credentialsProvider.notifier).load();

    final notifier = container.read(credentialsProvider.notifier);
    expect(notifier.of('https://example.com')!.user, 'alice');
    expect(notifier.of('http://192.168.1.5:8080')!.token, 'flr_b');
    expect(notifier.of('https://other.example'), isNull);
  });

  test(
    'ignores other entries in the store, and entries it cannot read',
    () async {
      final store = MemorySecretStore()
        ..values['something-else'] = alice.toJson()
        ..values['credentials:https://broken.example'] = 'not json'
        ..values['credentials:https://partial.example'] = '{"token":"x"}'
        ..values['credentials:https://ok.example'] = bob.toJson();
      final container = containerWith(store);
      await container.read(credentialsProvider.notifier).load();

      expect(container.read(credentialsProvider).byServer.keys, [
        'https://ok.example',
      ]);
    },
  );

  test('saves a sign-in in the store and in memory', () async {
    final store = MemorySecretStore();
    final container = containerWith(store);
    final notifier = container.read(credentialsProvider.notifier);
    await notifier.save('https://example.com', alice);

    expect(notifier.of('https://example.com')!.user, 'alice');
    expect(
      Credentials.fromJson(store.values['credentials:https://example.com']!)!
          .token,
      'flr_a',
    );
    expect(container.read(credentialsProvider).persistent, isTrue);
  });

  test(
    'keeps a sign-in for each server, and replaces one on the same server',
    () async {
      final container = containerWith(MemorySecretStore());
      final notifier = container.read(credentialsProvider.notifier);
      await notifier.save('https://a.example', alice);
      await notifier.save('https://b.example', bob);
      await notifier.save('https://a.example', bob);

      expect(notifier.of('https://a.example')!.user, 'bob');
      expect(notifier.of('https://b.example')!.user, 'bob');
      expect(container.read(credentialsProvider).byServer, hasLength(2));
    },
  );

  test('forgets a sign-in everywhere', () async {
    final store = MemorySecretStore();
    final container = containerWith(store);
    final notifier = container.read(credentialsProvider.notifier);
    await notifier.save('https://example.com', alice);
    await notifier.save('https://other.example', bob);
    await notifier.remove('https://example.com');

    expect(notifier.of('https://example.com'), isNull);
    expect(store.values.keys, ['credentials:https://other.example']);
  });

  test('round-trips through JSON', () {
    final back = Credentials.fromJson(alice.toJson())!;
    expect((back.token, back.tokenId, back.user), ('flr_a', 'tok_a', 'alice'));
  });

  group('when the secure storage cannot be used', () {
    test('loads nothing and says sign-ins will not be kept', () async {
      final container = containerWith(BrokenSecretStore());
      await container.read(credentialsProvider.notifier).load();
      final state = container.read(credentialsProvider);
      expect(state.byServer, isEmpty);
      expect(state.persistent, isFalse);
    });

    test('still lets the user sign in, for this run', () async {
      final container = containerWith(BrokenSecretStore());
      final notifier = container.read(credentialsProvider.notifier);
      await notifier.save('https://example.com', alice);

      expect(notifier.of('https://example.com')!.user, 'alice');
      expect(container.read(credentialsProvider).persistent, isFalse);
    });

    test('still signs out', () async {
      final container = containerWith(BrokenSecretStore());
      final notifier = container.read(credentialsProvider.notifier);
      await notifier.save('https://example.com', alice);
      await notifier.remove('https://example.com');
      expect(notifier.of('https://example.com'), isNull);
    });
  });

  group('when the secure storage never answers', () {
    test('starts anyway, with no sign-ins and a warning that they will not be kept', () async {
      final container = containerWith(HangingSecretStore());
      await container.read(credentialsProvider.notifier).load();

      expect(container.read(credentialsProvider).byServer, isEmpty);
      expect(container.read(credentialsProvider).persistent, isFalse);
    });

    test('still lets the user sign in and out, for this run', () async {
      final container = containerWith(HangingSecretStore());
      final notifier = container.read(credentialsProvider.notifier);
      await notifier.save('https://example.com', alice);
      expect(notifier.of('https://example.com')!.user, 'alice');
      expect(container.read(credentialsProvider).persistent, isFalse);

      await notifier.remove('https://example.com');
      expect(notifier.of('https://example.com'), isNull);
    });
  });
}
