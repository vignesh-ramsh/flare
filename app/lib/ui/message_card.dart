import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/db/database.dart';
import '../l10n/app_localizations.dart';
import '../app/theme.dart';
import 'link_opener.dart';
import 'priority.dart';
import 'time_format.dart';

/// A message as a card: priority, title, text, tags, link and time. Its text is
/// plain [Text]: wrap a list of cards in a [SelectionArea] to let users copy it.
class MessageCard extends ConsumerWidget {
  const MessageCard({required this.message, this.topicLabel, super.key});

  final MessageRow message;

  /// Names the topic when messages from several are listed together.
  final String? topicLabel;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final title = message.title;
    final link = webLink(message.click);
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
              if (link != null)
                _ClickLink(link: link)
              else
                // Not a web address, so it is shown and never opened.
                Text(
                  message.click!,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: scheme.onSurfaceVariant,
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

/// The address a message asks to open, as a link that opens it in the browser.
class _ClickLink extends ConsumerWidget {
  const _ClickLink({required this.link});

  final Uri link;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final messenger = ScaffoldMessenger.maybeOf(context);
    final failed = AppLocalizations.of(context).messageLinkOpenFailed;
    return Semantics(
      link: true,
      child: InkWell(
        borderRadius: BorderRadius.circular(4),
        onTap: () async {
          final opened = await ref.read(linkOpenerProvider).open(link);
          if (!opened) messenger?.showSnackBar(SnackBar(content: Text(failed)));
        },
        child: ConstrainedBox(
          constraints: BoxConstraints(
            minHeight: theme.extension<PlatformTokens>()?.targetSize ?? 48,
          ),
          child: Align(
            alignment: AlignmentDirectional.centerStart,
            child: Text(
              link.toString(),
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.primary,
                decoration: TextDecoration.underline,
              ),
            ),
          ),
        ),
      ),
    );
  }
}
