import 'package:flutter/material.dart';

import '../../core/db/database.dart';
import '../../l10n/app_localizations.dart';
import '../../ui/priority.dart';
import '../../ui/time_format.dart';

/// The user's own message: a bubble on the right, unlike the cards others' messages are.
class MessageBubble extends StatelessWidget {
  const MessageBubble({required this.message, super.key});

  final MessageRow message;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final title = message.title;
    final time = DateTime.fromMillisecondsSinceEpoch(message.time * 1000);

    return Align(
      alignment: Alignment.centerRight,
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxWidth: MediaQuery.sizeOf(context).width * 0.8,
        ),
        child: Semantics(
          label: l10n.messageFromYou,
          container: true,
          child: DecoratedBox(
            decoration: BoxDecoration(
              color: scheme.primaryContainer,
              borderRadius: const BorderRadius.only(
                topLeft: Radius.circular(16),
                topRight: Radius.circular(16),
                bottomLeft: Radius.circular(16),
                bottomRight: Radius.circular(4),
              ),
            ),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.end,
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (message.priority != 3)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 4),
                      child: PriorityBadge(priority: message.priority),
                    ),
                  if (title != null && title.isNotEmpty)
                    Text(
                      title,
                      style: theme.textTheme.titleSmall?.copyWith(
                        color: scheme.onPrimaryContainer,
                      ),
                    ),
                  if (message.body.isNotEmpty)
                    Text(
                      message.body,
                      style: theme.textTheme.bodyMedium?.copyWith(
                        color: scheme.onPrimaryContainer,
                      ),
                    ),
                  const SizedBox(height: 4),
                  Text(
                    formatFullTime(context, time),
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: scheme.onPrimaryContainer,
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
