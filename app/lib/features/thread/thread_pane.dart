import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/routes.dart';
import '../../app/window_class.dart';
import '../../core/db/database.dart';
import '../../data/models.dart';
import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../ui/empty_state.dart';
import '../../ui/message_card.dart';
import '../../notifications/viewing.dart';
import '../topics/connection_banner.dart';
import 'composer.dart';
import 'message_bubble.dart';
import 'topic_notifications_dialog.dart';

/// Whether the large layout shows the details pane beside the thread.
class DetailsVisible extends Notifier<bool> {
  @override
  bool build() => true;

  void toggle() => state = !state;
}

final detailsVisibleProvider = NotifierProvider<DetailsVisible, bool>(
  DetailsVisible.new,
);

/// A topic's messages, newest at the bottom, as cards.
class ThreadPane extends ConsumerWidget {
  const ThreadPane({required this.topic, this.showBack = false, super.key});

  final TopicRef topic;

  /// Compact and medium windows show the thread over the list, with a back arrow.
  final bool showBack;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final summary = ref.watch(topicSummaryProvider(topic));

    return summary.when(
      loading: () =>
          Scaffold(appBar: _appBar(context, ref, topic.name, topic.host)),
      error: (error, stack) => Scaffold(
        appBar: _appBar(context, ref, topic.name, topic.host),
        body: Center(child: Text('$error')),
      ),
      data: (t) {
        if (t == null) {
          return Scaffold(
            appBar: _appBar(context, ref, topic.name, topic.host),
            body: EmptyState(icon: Icons.link_off, title: l10n.topicNotFound),
          );
        }
        return _Thread(
          key: ValueKey('${t.host}/${t.name}'),
          topic: t,
          appBar: _appBar(context, ref, t.name, t.host),
        );
      },
    );
  }

  PreferredSizeWidget _appBar(
    BuildContext context,
    WidgetRef ref,
    String name,
    String host,
  ) {
    final l10n = AppLocalizations.of(context);
    final detailsVisible = ref.watch(detailsVisibleProvider);
    return AppBar(
      automaticallyImplyLeading: false,
      leading: showBack
          ? BackButton(
              onPressed: () =>
                  context.canPop() ? context.pop() : context.go('/'),
            )
          : null,
      title: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(name, maxLines: 1, overflow: TextOverflow.ellipsis),
          Text(
            host,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
      actions: [
        IconButton(
          tooltip: l10n.topicNotifications,
          icon: const Icon(Icons.notifications_outlined),
          onPressed: () => TopicNotificationsDialog.show(context, topic),
        ),
        if (context.windowClass != WindowClass.large)
          IconButton(
            tooltip: l10n.topicInfoTitle,
            icon: const Icon(Icons.info_outline),
            onPressed: () => context.push(topicInfoLocation(host, name)),
          )
        else
          IconButton(
            tooltip: detailsVisible ? l10n.hideDetails : l10n.showDetails,
            isSelected: detailsVisible,
            icon: const Icon(Icons.info_outline),
            selectedIcon: const Icon(Icons.info),
            onPressed: () => ref.read(detailsVisibleProvider.notifier).toggle(),
          ),
      ],
    );
  }
}

class _Thread extends ConsumerStatefulWidget {
  const _Thread({required this.topic, required this.appBar, super.key});

  final TopicSummary topic;
  final PreferredSizeWidget appBar;

  @override
  ConsumerState<_Thread> createState() => _ThreadState();
}

class _ThreadState extends ConsumerState<_Thread> {
  late final ViewedTopic _viewed;
  late final TopicRef _ref;

  @override
  void dispose() {
    // A provider must not change while the widget tree is being torn down.
    Future.microtask(() {
      try {
        _viewed.leave(_ref);
      } on Object {
        // The app is closing and the provider is already gone.
      }
    });
    super.dispose();
  }

  @override
  void initState() {
    super.initState();
    _viewed = ref.read(viewedTopicProvider.notifier);
    _ref = (host: widget.topic.host, name: widget.topic.name);
    // After the frame: a provider must not change while widgets are being built.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _viewed.show(_ref);
    });
    // Showing the newest message is what reads it: whenever the thread on screen
    // holds messages the marker is behind, move the marker.
    final topic = widget.topic;
    ref.listenManual(
      topicMessagesProvider((serverId: topic.serverId, name: topic.name)),
      (previous, next) {
        final messages = next.value;
        if (messages != null && messages.isNotEmpty) {
          ref
              .read(topicsRepositoryProvider)
              .markRead(topic.serverId, topic.name);
        }
      },
      fireImmediately: true,
    );
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final topic = widget.topic;
    final messages = ref.watch(
      topicMessagesProvider((serverId: topic.serverId, name: topic.name)),
    );

    return Scaffold(
      appBar: widget.appBar,
      body: Column(
        children: [
          // Where the list is beside the thread (expanded, large) it already says so.
          if (!context.windowClass.hasTwoPanes)
            ConnectionBanner(serverId: topic.serverId),
          TopicRefusedBanner(serverId: topic.serverId, topic: topic.name),
          Expanded(child: _messages(context, l10n, messages)),
          Composer(topic: topic),
        ],
      ),
    );
  }

  Widget _messages(
    BuildContext context,
    AppLocalizations l10n,
    AsyncValue<List<MessageRow>> messages,
  ) {
    return messages.when(
      loading: () => const SizedBox.shrink(),
      error: (error, stack) => Center(child: Text('$error')),
      data: (list) {
        if (list.isEmpty) {
          return EmptyState(
            icon: Icons.chat_bubble_outline,
            title: l10n.threadEmpty,
          );
        }
        return Align(
          alignment: Alignment.topCenter,
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 720),
            child: SelectionArea(
              child: ListView.separated(
                reverse: true,
                padding: const EdgeInsets.all(16),
                itemCount: list.length,
                separatorBuilder: (context, index) => const SizedBox(height: 8),
                itemBuilder: (context, index) => list[index].mine
                    ? MessageBubble(message: list[index])
                    : MessageCard(message: list[index]),
              ),
            ),
          ),
        );
      },
    );
  }
}
