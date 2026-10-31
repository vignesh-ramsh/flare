import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../auth/credentials.dart';
import '../net/providers.dart';
import 'messages_repository.dart';
import 'providers.dart';
import 'server_url.dart';
import 'servers_repository.dart';

/// A build locked to one server always has it, so the user never has to enter it and the
/// first-run pages are skipped.
Future<void> ensureLockedServer(
  ServersRepository servers,
  ServerPolicy policy,
) async {
  if (policy.allowCustom) return;
  final address = ServerAddress.parse(policy.defaultUrl);
  if (address != null) await servers.add(address);
}

/// Everything the app does before its first screen: reads the saved sign-ins, makes sure a
/// locked build has its server, drops messages past the history kept, and starts following
/// the subscriptions.
Future<void> startServices(ProviderContainer container) async {
  await container.read(credentialsProvider.notifier).load();
  await ensureLockedServer(
    container.read(serversRepositoryProvider),
    container.read(serverPolicyProvider),
  );
  // So the first screen already knows whether to show the first-run pages. A provider
  // only runs while something listens to it, so listen for as long as the app lives:
  // awaiting `.future` alone would wait forever.
  container.listen(serversProvider, (previous, next) {});
  await container.read(serversProvider.future);
  await container
      .read(messagesRepositoryProvider)
      .pruneOlderThan(container.read(clockProvider)().subtract(defaultHistory));
  container.read(connectionManagerProvider).start();
}
