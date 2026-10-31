import 'package:flutter/material.dart';

import '../core/db/database.dart';
import 'priority.dart';
import 'time_format.dart';

/// A message as a card: priority, title, text, tags, link and time. Its text is
/// plain [Text]: wrap a list of cards in a [SelectionArea] to let users copy it.
class MessageCard extends StatelessWidget {
  const MessageCard({required this.message, this.topicLabel, super.key});

  final MessageRow message;

  /// Names the topic when messages from several are listed together.
  final String? topicLabel;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final title = message.title;
    final time = DateTime.fromMillisecondsSinceEpoch(message.time * 1000);

    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (topicLabel != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 4),
                child: Text(
                  topicLabel!,
                  style: theme.textTheme.labelMedium?.copyWith(
                    color: scheme.onSurfaceVariant,
                  ),
                ),
              ),
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (title != null && title.isNotEmpty)
                  Expanded(
                    child: Text(title, style: theme.textTheme.titleMedium),
                  )
                else
                  const Spacer(),
                PriorityBadge(priority: message.priority),
              ],
            ),
            if (message.body.isNotEmpty) ...[
              const SizedBox(height: 4),
              Text(message.body, style: theme.textTheme.bodyMedium),
            ],
            if (message.tags.isNotEmpty) ...[
              const SizedBox(height: 8),
              Wrap(
                spacing: 8,
                runSpacing: 4,
                children: [
                  for (final tag in message.tags)
                    Chip(
                      label: Text(tag),
                      visualDensity: VisualDensity.compact,
                      materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                ],
              ),
            ],
            if (message.click != null && message.click!.isNotEmpty) ...[
              const SizedBox(height: 8),
              Text(
                message.click!,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: scheme.primary,
                  decoration: TextDecoration.underline,
                ),
              ),
            ],
            const SizedBox(height: 8),
            Text(
              formatFullTime(context, time),
              style: theme.textTheme.labelSmall?.copyWith(
                color: scheme.onSurfaceVariant,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
