import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../auth/credentials.dart';
import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../ui/empty_state.dart';

/// The servers the user subscribes through, with whether they are signed in to each.
class ServersScreen extends ConsumerWidget {
  const ServersScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final servers = ref.watch(serversWithCountsProvider);
    final credentials = ref.watch(credentialsProvider).byServer;
    final allowCustom = ref.watch(serverPolicyProvider).allowCustom;

    return Scaffold(
      appBar: AppBar(
        leading: BackButton(
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/settings'),
        ),
        title: Text(l10n.settingsServers),
        actions: [
          if (allowCustom)
            IconButton(
              tooltip: l10n.serversAdd,
              icon: const Icon(Icons.add),
              onPressed: () => context.push('/add-server'),
            ),
        ],
      ),
      body: servers.when(
        loading: () => const SizedBox.shrink(),
        error: (error, stack) => Center(child: Text('$error')),
        data: (list) {
          if (list.isEmpty) {
            return EmptyState(
              icon: Icons.dns_outlined,
              title: l10n.serversEmpty,
            );
          }
          return Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 720),
              child: ListView(
                padding: const EdgeInsets.symmetric(vertical: 8),
                children: [
                  for (final entry in list)
                    ListTile(
                      leading: const Icon(Icons.dns_outlined),
                      title: Text(entry.server.host),
                      subtitle: Text(
                        [
                          l10n.serverTopicCount(entry.topics),
                          if (credentials[entry.server.url]
                              case final signedIn?)
                            l10n.serverSignedInAs(signedIn.user)
                          else
                            l10n.serverNotSignedIn,
                        ].join(' · '),
                      ),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () =>
                          context.go('/settings/servers/${entry.server.id}'),
                    ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}
