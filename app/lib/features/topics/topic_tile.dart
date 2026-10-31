import 'package:flutter/material.dart';

import '../../data/models.dart';
import '../../l10n/app_localizations.dart';
import '../../ui/time_format.dart';
import '../../ui/unread_badge.dart';

/// One subscription in the topic list: avatar, name, time, preview and unread count.
/// Laid out by hand rather than as a ListTile, so large text sizes grow it instead
/// of overflowing.
class TopicTile extends StatelessWidget {
  const TopicTile({
    required this.topic,
    required this.now,
    required this.onTap,
    this.selected = false,
    this.showServer = false,
    super.key,
  });

  final TopicSummary topic;
  final DateTime now;
  final VoidCallback onTap;
  final bool selected;

  /// Names the server too, when topics from several are listed together.
  final bool showServer;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final last = topic.last;
    final preview = last?.preview ?? l10n.noMessagesYet;
    final unread = topic.muted ? 0 : topic.unread;
    final dim = theme.textTheme.bodyMedium?.copyWith(
      color: scheme.onSurfaceVariant,
    );

    final spoken = [
      showServer ? l10n.topicOnServer(topic.name, topic.host) : topic.name,
      if (topic.muted) l10n.topicMuted,
      if (unread > 0) l10n.unreadMessages(unread),
      preview,
    ].join(', ');

    return Semantics(
      label: spoken,
      button: true,
      selected: selected,
      onTap: onTap,
      excludeSemantics: true,
      child: Material(
        color: selected ? scheme.secondaryContainer : Colors.transparent,
        borderRadius: BorderRadius.circular(12),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          child: ConstrainedBox(
            constraints: const BoxConstraints(minHeight: 72),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  CircleAvatar(
                    backgroundColor: scheme.primaryContainer,
                    foregroundColor: scheme.onPrimaryContainer,
                    child: Text(topic.name.characters.first.toUpperCase()),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          crossAxisAlignment: CrossAxisAlignment.baseline,
                          textBaseline: TextBaseline.alphabetic,
                          children: [
                            // The name and the muted mark take the free space, so the
                            // time sits at the right edge, level with the badge below.
                            Expanded(
                              child: Row(
                                children: [
                                  Flexible(
                                    child: Text(
                                      topic.name,
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: theme.textTheme.titleMedium
                                          ?.copyWith(
                                            fontWeight: unread > 0
                                                ? FontWeight.w700
                                                : null,
                                          ),
                                    ),
                                  ),
                                  if (topic.muted) ...[
                                    const SizedBox(width: 8),
                                    Icon(
                                      Icons.notifications_off_outlined,
                                      size: 16,
                                      color: scheme.onSurfaceVariant,
                                    ),
                                  ],
                                ],
                              ),
                            ),
                            if (last != null) ...[
                              const SizedBox(width: 8),
                              Text(
                                formatListTime(context, last.time, now),
                                style: theme.textTheme.labelSmall?.copyWith(
                                  color: scheme.onSurfaceVariant,
                                ),
                              ),
                            ],
                          ],
                        ),
                        if (showServer)
                          Text(
                            topic.host,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: theme.textTheme.bodySmall?.copyWith(
                              color: scheme.onSurfaceVariant,
                            ),
                          ),
                        const SizedBox(height: 2),
                        Row(
                          crossAxisAlignment: CrossAxisAlignment.center,
                          children: [
                            Expanded(
                              child: Text(
                                preview,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: dim,
                              ),
                            ),
                            if (unread > 0) ...[
                              const SizedBox(width: 8),
                              UnreadBadge(count: unread),
                            ],
                          ],
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
