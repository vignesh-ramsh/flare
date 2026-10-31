import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../brand/brand.g.dart';
import '../data/models.dart';
import '../data/providers.dart';
import '../l10n/app_localizations.dart';
import '../features/topics/connection_banner.dart';
import '../ui/brand_logo.dart';
import '../ui/unread_badge.dart';
import 'routes.dart';
import 'theme.dart';
import 'window_class.dart';

enum Destination {
  topics('/', Icons.notifications_outlined, Icons.notifications),
  inbox('/inbox', Icons.inbox_outlined, Icons.inbox),
  settings('/settings', Icons.settings_outlined, Icons.settings);

  const Destination(this.path, this.icon, this.selectedIcon);

  final String path;
  final IconData icon;
  final IconData selectedIcon;

  String label(AppLocalizations l10n) => switch (this) {
    topics => l10n.navTopics,
    inbox => l10n.navInbox,
    settings => l10n.navSettings,
  };

  static Destination of(String location) {
    if (location == '/inbox' || location.startsWith('/inbox/')) return inbox;
    if (location == '/settings' || location.startsWith('/settings/')) {
      return settings;
    }
    return topics;
  }
}

/// A thread or a settings page opened over its list. Compact windows hide the
/// bottom bar there, so the page has the whole screen and a back arrow.
bool _isDetailLocation(String location) =>
    location.startsWith('/topic/') || location.startsWith('/settings/');

/// The navigation around every screen: a bottom bar, a rail or a sidebar,
/// depending on the window's width.
class AppShell extends ConsumerWidget {
  const AppShell({required this.location, required this.child, super.key});

  final String location;
  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final current = Destination.of(location);
    void select(int index) {
      final target = Destination.values[index];
      if (target != current) context.go(target.path);
    }

    return switch (context.windowClass) {
      WindowClass.compact => Scaffold(
        body: child,
        bottomNavigationBar: _isDetailLocation(location)
            ? null
            : NavigationBar(
                selectedIndex: current.index,
                onDestinationSelected: select,
                destinations: [
                  for (final d in Destination.values)
                    NavigationDestination(
                      icon: _DestinationIcon(d, selected: false),
                      selectedIcon: _DestinationIcon(d, selected: true),
                      label: d.label(l10n),
                    ),
                ],
              ),
      ),
      WindowClass.medium || WindowClass.expanded => Scaffold(
        body: Row(
          children: [
            NavigationRail(
              selectedIndex: current.index,
              onDestinationSelected: select,
              labelType: NavigationRailLabelType.all,
              leading: const Padding(
                padding: EdgeInsets.symmetric(vertical: 12),
                child: BrandLogo(size: 40),
              ),
              destinations: [
                for (final d in Destination.values)
                  NavigationRailDestination(
                    icon: _DestinationIcon(d, selected: false),
                    selectedIcon: _DestinationIcon(d, selected: true),
                    label: Text(d.label(l10n)),
                  ),
              ],
            ),
            const VerticalDivider(width: 1),
            Expanded(child: child),
          ],
        ),
      ),
      WindowClass.large => Scaffold(
        body: Row(
          children: [
            _Sidebar(location: location),
            const VerticalDivider(width: 1),
            Expanded(child: child),
          ],
        ),
      ),
    };
  }
}

/// A destination's icon, with the unread total on Topics.
class _DestinationIcon extends ConsumerWidget {
  const _DestinationIcon(this.destination, {required this.selected});

  final Destination destination;
  final bool selected;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final icon = Icon(selected ? destination.selectedIcon : destination.icon);
    if (destination != Destination.topics) return icon;

    final unread = ref
        .watch(topicSummariesProvider)
        .maybeWhen(
          data: (topics) =>
              topics.fold<int>(0, (sum, t) => sum + (t.muted ? 0 : t.unread)),
          orElse: () => 0,
        );
    final accent =
        Theme.of(context).extension<PlatformTokens>()?.accent ?? brandColor;
    return Badge(
      isLabelVisible: unread > 0,
      backgroundColor: accent,
      textColor: onColor(accent),
      label: Text(unread > 99 ? '99+' : '$unread'),
      child: icon,
    );
  }
}

/// The large layout's sidebar: servers and their topics as a tree, then Inbox and Settings.
class _Sidebar extends ConsumerWidget {
  const _Sidebar({required this.location});

  final String location;

  static const double width = 280;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final topics = ref.watch(topicSummariesProvider).value ?? const [];
    final current = Destination.of(location);

    final byServer = <String, List<TopicSummary>>{};
    for (final t in topics) {
      byServer.putIfAbsent(t.host, () => []).add(t);
    }

    return SizedBox(
      width: width,
      child: Material(
        color: theme.colorScheme.surfaceContainerLow,
        child: SafeArea(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: Row(
                  children: [
                    const BrandLogo(size: 32),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Text(
                        Brand.name,
                        style: theme.textTheme.titleLarge,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 8,
                ),
                child: FilledButton.tonalIcon(
                  onPressed: () => context.push('/add'),
                  icon: const Icon(Icons.add),
                  label: Text(l10n.subscribe),
                ),
              ),
              const ConnectionBanner(),
              Expanded(
                child: ListView(
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                  children: [
                    for (final entry in byServer.entries) ...[
                      Padding(
                        padding: const EdgeInsets.fromLTRB(12, 16, 12, 4),
                        child: Text(
                          entry.key,
                          style: theme.textTheme.labelMedium?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      for (final topic in entry.value)
                        _SidebarTopic(
                          topic: topic,
                          selected:
                              location == topicLocation(topic.host, topic.name),
                        ),
                    ],
                  ],
                ),
              ),
              const Divider(),
              Padding(
                padding: const EdgeInsets.all(8),
                child: Column(
                  children: [
                    for (final d in [Destination.inbox, Destination.settings])
                      ListTile(
                        dense: true,
                        selected: current == d,
                        selectedTileColor: theme.colorScheme.secondaryContainer,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(8),
                        ),
                        leading: Icon(current == d ? d.selectedIcon : d.icon),
                        title: Text(d.label(l10n)),
                        onTap: () => context.go(d.path),
                      ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _SidebarTopic extends StatelessWidget {
  const _SidebarTopic({required this.topic, required this.selected});

  final TopicSummary topic;
  final bool selected;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return ListTile(
      dense: true,
      selected: selected,
      selectedTileColor: scheme.secondaryContainer,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
      title: Text(topic.name, overflow: TextOverflow.ellipsis),
      trailing: topic.muted
          ? Icon(
              Icons.notifications_off_outlined,
              size: 16,
              color: scheme.onSurfaceVariant,
            )
          : UnreadBadge(count: topic.unread),
      onTap: () => context.go(topicLocation(topic.host, topic.name)),
    );
  }
}
