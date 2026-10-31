import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/window_class.dart';
import '../../data/models.dart';
import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../ui/empty_state.dart';
import '../thread/details_pane.dart';
import '../thread/thread_pane.dart';
import 'topic_list_pane.dart';

/// The Topics destination. The same page serves `/` and `/topic/:server/:name`,
/// and lays itself out by window class:
///
/// * compact, medium: the list, or the thread over it when a topic is open;
/// * expanded: the list (360 dp) and the thread side by side;
/// * large: the thread and a details pane; the sidebar is the list.
class TopicsPage extends ConsumerWidget {
  const TopicsPage({this.selected, super.key});

  final TopicRef? selected;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final open = selected;
    return switch (context.windowClass) {
      WindowClass.compact || WindowClass.medium =>
        open == null
            ? const TopicListPane()
            : ThreadPane(topic: open, showBack: true),
      WindowClass.expanded => Row(
        children: [
          SizedBox(width: 360, child: TopicListPane(selected: open)),
          const VerticalDivider(width: 1),
          Expanded(
            child: open == null
                ? const _SelectTopic()
                : ThreadPane(topic: open),
          ),
        ],
      ),
      WindowClass.large => Row(
        children: [
          Expanded(
            child: open == null
                ? const _SelectTopic(noTopicsHint: true)
                : ThreadPane(topic: open),
          ),
          if (open != null && ref.watch(detailsVisibleProvider)) ...[
            const VerticalDivider(width: 1),
            DetailsPane(topic: open),
          ],
        ],
      ),
    };
  }
}

/// What the thread pane shows before a topic is chosen. [noTopicsHint] adds how to
/// start when there are no topics at all; the large layout needs it because it has
/// no list pane to say so.
class _SelectTopic extends ConsumerWidget {
  const _SelectTopic({this.noTopicsHint = false});

  final bool noTopicsHint;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final none =
        noTopicsHint &&
        (ref.watch(topicSummariesProvider).value?.isEmpty ?? false);

    return Scaffold(
      body: none
          ? EmptyState(
              icon: Icons.notifications_none,
              title: l10n.topicsEmptyTitle,
              body: l10n.topicsEmptyBody,
              action: FilledButton.icon(
                onPressed: () => context.push('/add'),
                icon: const Icon(Icons.add),
                label: Text(l10n.subscribe),
              ),
            )
          : EmptyState(
              icon: Icons.forum_outlined,
              title: l10n.selectTopicTitle,
              body: l10n.selectTopicBody,
            ),
    );
  }
}
