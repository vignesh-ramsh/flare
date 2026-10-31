import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/shell.dart';
import '../../app/window_class.dart';
import '../../data/models.dart';
import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../ui/empty_state.dart';
import 'topic_tile.dart';

/// The list of subscriptions, with the server filter and the Subscribe button.
/// A full page in compact and medium windows, a 360 dp pane in expanded ones.
class TopicListPane extends ConsumerWidget {
  const TopicListPane({this.selected, super.key});

  final TopicRef? selected;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final windowClass = context.windowClass;
    final topics = ref.watch(topicSummariesProvider);
    final servers = ref.watch(serversProvider).value ?? const [];
    final filter = ref.watch(serverFilterProvider);
    final now = ref.watch(clockProvider)();

    final subscribeButton = windowClass == WindowClass.expanded
        ? IconButton(
            tooltip: l10n.subscribe,
            icon: const Icon(Icons.add),
            onPressed: () => context.push('/add'),
          )
        : null;

    return Scaffold(
      appBar: AppBar(
        automaticallyImplyLeading: false,
        title: Text(l10n.topicsTitle),
        actions: [?subscribeButton],
      ),
      floatingActionButton: windowClass.hasTwoPanes
          ? null
          : FloatingActionButton.extended(
              onPressed: () => context.push('/add'),
              icon: const Icon(Icons.add),
              label: Text(l10n.subscribe),
            ),
      body: topics.when(
        loading: () => const SizedBox.shrink(),
        error: (error, stack) => Center(child: Text('$error')),
        data: (all) {
          if (all.isEmpty) {
            return EmptyState(
              icon: Icons.notifications_none,
              title: l10n.topicsEmptyTitle,
              body: l10n.topicsEmptyBody,
            );
          }
          final shown = [
            for (final t in all)
              if (filter == null || t.serverId == filter) t,
          ];
          return Column(
            children: [
              if (servers.length > 1)
                SizedBox(
                  height: 56,
                  child: ListView(
                    scrollDirection: Axis.horizontal,
                    padding: const EdgeInsets.symmetric(
                      horizontal: 16,
                      vertical: 8,
                    ),
                    children: [
                      ChoiceChip(
                        label: Text(l10n.allServers),
                        selected: filter == null,
                        onSelected: (_) => ref
                            .read(serverFilterProvider.notifier)
                            .select(null),
                      ),
                      for (final server in servers) ...[
                        const SizedBox(width: 8),
                        ChoiceChip(
                          label: Text(server.host),
                          selected: filter == server.id,
                          onSelected: (_) => ref
                              .read(serverFilterProvider.notifier)
                              .select(server.id),
                        ),
                      ],
                    ],
                  ),
                ),
              Expanded(
                child: ListView.builder(
                  padding: const EdgeInsets.fromLTRB(8, 4, 8, 88),
                  itemCount: shown.length,
                  itemBuilder: (context, index) {
                    final topic = shown[index];
                    return Padding(
                      padding: const EdgeInsets.symmetric(vertical: 2),
                      child: TopicTile(
                        topic: topic,
                        now: now,
                        showServer: filter == null && servers.length > 1,
                        selected:
                            selected?.host == topic.host &&
                            selected?.name == topic.name,
                        onTap: () =>
                            context.go(topicLocation(topic.host, topic.name)),
                      ),
                    );
                  },
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}
