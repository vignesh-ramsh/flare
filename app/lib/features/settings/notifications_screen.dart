import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';

import '../../app/theme.dart';
import '../../data/providers.dart';
import '../../data/settings_repository.dart';
import '../../desktop/window_actions.dart';
import '../../l10n/app_localizations.dart';

/// Settings > Notifications: whether and how messages announce themselves.
class NotificationsScreen extends ConsumerWidget {
  const NotificationsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final prefs =
        ref.watch(notificationPrefsProvider).value ?? const NotificationPrefs();
    final settings = ref.read(settingsRepositoryProvider);
    final now = ref.watch(clockProvider)();
    final paused = prefs.pausedAt(now);
    final desktop = !kIsWeb && isDesktopPlatform(defaultTargetPlatform);
    final tray = ref.watch(trayAvailableProvider);

    return Scaffold(
      appBar: AppBar(
        leading: BackButton(
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/settings'),
        ),
        title: Text(l10n.notificationsTitle),
      ),
      body: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: ListView(
            padding: const EdgeInsets.symmetric(vertical: 8),
            children: [
              SwitchListTile(
                title: Text(l10n.notificationsShow),
                value: prefs.enabled,
                onChanged: settings.setEnabled,
              ),
              SwitchListTile(
                title: Text(l10n.notificationsSound),
                value: prefs.sound,
                onChanged: prefs.enabled ? settings.setSound : null,
              ),
              SwitchListTile(
                title: Text(l10n.notificationsContent),
                subtitle: Text(l10n.notificationsContentHelp),
                value: prefs.showContent,
                onChanged: prefs.enabled ? settings.setShowContent : null,
              ),
              ListTile(
                title: Text(
                  paused
                      ? l10n.notificationsPausedUntil(
                          DateFormat.jm(
                            Localizations.localeOf(context).toString(),
                          ).format(prefs.pausedUntil!),
                        )
                      : l10n.notificationsPause,
                ),
                trailing: paused
                    ? TextButton(
                        onPressed: () => settings.setPausedUntil(null),
                        child: Text(l10n.notificationsResume),
                      )
                    : TextButton(
                        onPressed: prefs.enabled
                            ? () => settings.setPausedUntil(
                                now.add(const Duration(hours: 1)),
                              )
                            : null,
                        child: Text(l10n.notificationsPause),
                      ),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
                child: Text(
                  l10n.notificationsPriorityNote,
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
              if (desktop) ...[
                const Divider(),
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                  child: Text(
                    l10n.notificationsDesktop,
                    style: Theme.of(context).textTheme.titleSmall,
                  ),
                ),
                SwitchListTile(
                  title: Text(l10n.notificationsCloseToTray),
                  value: prefs.closeToTray && tray != false,
                  onChanged: tray == false ? null : settings.setCloseToTray,
                ),
                if (tray == false)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                    child: Text(
                      l10n.notificationsNoTray,
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
