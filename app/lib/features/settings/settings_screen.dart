import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';

/// Settings: the sections the app has so far.
class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final servers = ref.watch(serversProvider).value ?? const [];

    return Scaffold(
      appBar: AppBar(
        automaticallyImplyLeading: false,
        title: Text(l10n.settingsTitle),
      ),
      body: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: ListView(
            padding: const EdgeInsets.symmetric(vertical: 8),
            children: [
              ListTile(
                leading: const Icon(Icons.dns_outlined),
                title: Text(l10n.settingsServers),
                subtitle: Text(l10n.settingsServersCount(servers.length)),
                trailing: const Icon(Icons.chevron_right),
                onTap: () => context.go('/settings/servers'),
              ),
              ListTile(
                leading: const Icon(Icons.notifications_outlined),
                title: Text(l10n.notificationsTitle),
                trailing: const Icon(Icons.chevron_right),
                onTap: () => context.go('/settings/notifications'),
              ),
              ListTile(
                leading: const Icon(Icons.info_outline),
                title: Text(l10n.settingsAbout),
                trailing: const Icon(Icons.chevron_right),
                onTap: () => context.go('/settings/about'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
