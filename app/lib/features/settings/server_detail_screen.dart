import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../auth/auth_service.dart';
import '../../auth/credentials.dart';
import '../../core/db/database.dart';
import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../net/providers.dart';
import '../../net/server_status.dart';
import '../../ui/empty_state.dart';

/// One server: its address, the state of its connection, the account, and removing it.
class ServerDetailScreen extends ConsumerWidget {
  const ServerDetailScreen({required this.serverId, super.key});

  final int serverId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final server = ref.watch(serverProvider(serverId));

    return Scaffold(
      appBar: AppBar(
        leading: BackButton(
          onPressed: () => context.canPop()
              ? context.pop()
              : context.go('/settings/servers'),
        ),
        title: Text(server.value?.host ?? l10n.serverDetailTitle),
      ),
      body: server.when(
        loading: () => const SizedBox.shrink(),
        error: (error, stack) => Center(child: Text('$error')),
        data: (row) => row == null
            ? EmptyState(icon: Icons.dns_outlined, title: l10n.serversEmpty)
            : _Body(server: row),
      ),
    );
  }
}

class _Body extends ConsumerWidget {
  const _Body({required this.server});

  final ServerRow server;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final credentialsState = ref.watch(credentialsProvider);
    final signedIn = credentialsState.byServer[server.url];
    final status = ref.watch(connectionStatusProvider).value?[server.id];
    final allowCustom = ref.watch(serverPolicyProvider).allowCustom;

    return Align(
      alignment: Alignment.topCenter,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720),
        child: ListView(
          padding: const EdgeInsets.symmetric(vertical: 8),
          children: [
            ListTile(
              title: Text(l10n.serverAddress),
              subtitle: SelectableText(server.url),
            ),
            ListTile(
              title: Text(l10n.serverConnection),
              subtitle: Text(_statusText(l10n, status)),
            ),
            const Divider(),
            ListTile(
              title: Text(l10n.serverAccount),
              subtitle: Text(
                signedIn == null
                    ? l10n.serverNotSignedIn
                    : l10n.serverSignedInAs(signedIn.user),
              ),
              trailing: signedIn == null
                  ? FilledButton(
                      onPressed: () => context.push('/signin/${server.id}'),
                      child: Text(l10n.signInSubmit),
                    )
                  : OutlinedButton(
                      onPressed: () => _signOut(context, ref),
                      child: Text(l10n.serverSignOut),
                    ),
            ),
            if (signedIn != null && !credentialsState.persistent)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                child: Text(
                  l10n.serverCredentialsNotSaved,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: theme.colorScheme.error,
                  ),
                ),
              ),
            if (allowCustom) ...[
              const Divider(),
              ListTile(
                leading: Icon(
                  Icons.delete_outline,
                  color: theme.colorScheme.error,
                ),
                title: Text(
                  l10n.serverRemove,
                  style: TextStyle(color: theme.colorScheme.error),
                ),
                onTap: () => _remove(context, ref),
              ),
            ],
          ],
        ),
      ),
    );
  }

  String _statusText(AppLocalizations l10n, ServerStatus? status) {
    return switch (status?.state) {
      LinkState.connected => l10n.linkConnected,
      LinkState.connecting => l10n.linkConnecting,
      LinkState.waiting => l10n.linkWaiting,
      LinkState.authFailed => l10n.linkAuthFailed,
      LinkState.idle || null => l10n.linkIdle,
    };
  }

  Future<void> _signOut(BuildContext context, WidgetRef ref) async {
    final l10n = AppLocalizations.of(context);
    final messenger = ScaffoldMessenger.of(context);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l10n.serverSignOutTitle(server.host)),
        content: Text(l10n.serverSignOutBody),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(l10n.cancel),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(l10n.serverSignOut),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    final revoked = await ref.read(authServiceProvider).signOut(server);
    if (!revoked) {
      messenger.showSnackBar(
        SnackBar(content: Text(l10n.serverSignedOutOffline(server.host))),
      );
    }
  }

  Future<void> _remove(BuildContext context, WidgetRef ref) async {
    final l10n = AppLocalizations.of(context);
    final router = GoRouter.of(context);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l10n.serverRemoveTitle(server.host)),
        content: Text(l10n.serverRemoveBody),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(l10n.cancel),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(l10n.serverRemoveAction),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    // Sign out first, so the token does not outlive the server entry on the server's side.
    await ref.read(authServiceProvider).signOut(server);
    await ref.read(serversRepositoryProvider).remove(server.id);
    router.go('/settings/servers');
  }
}
