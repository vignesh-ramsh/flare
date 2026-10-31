import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../data/models.dart';
import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';

/// The large layout's right-hand pane: facts about the open topic.
class DetailsPane extends ConsumerWidget {
  const DetailsPane({required this.topic, super.key});

  final TopicRef topic;

  static const double width = 320;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final summary = ref.watch(topicSummaryProvider(topic)).value;

    return SizedBox(
      width: width,
      child: Material(
        color: theme.colorScheme.surfaceContainerLow,
        child: SafeArea(
          child: SelectionArea(
            child: ListView(
              padding: const EdgeInsets.all(16),
              children: [
                Text(l10n.detailsTitle, style: theme.textTheme.titleMedium),
                const SizedBox(height: 16),
                _Fact(label: l10n.detailsServer, value: topic.host),
                if (summary != null) ...[
                  _Fact(
                    label: l10n.detailsMessages,
                    value: '${summary.messageCount}',
                  ),
                  _Fact(
                    label: l10n.detailsNotifications,
                    value: summary.muted
                        ? l10n.notificationsMuted
                        : l10n.notificationsOn,
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _Fact extends StatelessWidget {
  const _Fact({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: theme.textTheme.labelMedium?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 2),
          Text(value, style: theme.textTheme.bodyLarge),
        ],
      ),
    );
  }
}
