import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../ui/empty_state.dart';

/// The servers the user subscribes through.
class ServersScreen extends ConsumerWidget {
  const ServersScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final servers = ref.watch(serversWithCountsProvider);

    return Scaffold(
      appBar: AppBar(
        leading: BackButton(
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/settings'),
        ),
        title: Text(l10n.settingsServers),
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
                      subtitle: Text(l10n.serverTopicCount(entry.topics)),
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
