import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../data/models.dart';
import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';

/// A topic's own notification choices: mute it, or raise the bar for what announces itself.
class TopicNotificationsDialog extends ConsumerWidget {
  const TopicNotificationsDialog({required this.topic, super.key});

  final TopicRef topic;

  static Future<void> show(BuildContext context, TopicRef topic) =>
      showDialog<void>(
        context: context,
        builder: (context) => TopicNotificationsDialog(topic: topic),
      );

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final summary = ref.watch(topicSummaryProvider(topic)).value;
    final topics = ref.read(topicsRepositoryProvider);
    final words = {
      1: l10n.priorityMin,
      2: l10n.priorityLow,
      3: l10n.priorityDefault,
      4: l10n.priorityHigh,
      5: l10n.priorityUrgent,
    };

    return AlertDialog(
      title: Text(l10n.topicNotifications),
      content: summary == null
          ? const SizedBox.shrink()
          : SizedBox(
              width: 360,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  SwitchListTile(
                    contentPadding: EdgeInsets.zero,
                    title: Text(l10n.topicMute),
                    subtitle: Text(l10n.topicMuteHelp),
                    value: summary.muted,
                    onChanged: (value) =>
                        topics.setMuted(summary.topicId, value),
                  ),
                  const SizedBox(height: 8),
                  DropdownButtonFormField<int>(
                    initialValue: summary.minPriority,
                    decoration: InputDecoration(
                      labelText: l10n.topicMinPriority,
                    ),
                    items: [
                      for (final entry in words.entries)
                        DropdownMenuItem(
                          value: entry.key,
                          child: Text(entry.value),
                        ),
                    ],
                    onChanged: summary.muted
                        ? null
                        : (value) {
                            if (value != null) {
                              topics.setMinPriority(summary.topicId, value);
                            }
                          },
                  ),
                ],
              ),
            ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text(l10n.done),
        ),
      ],
    );
  }
}
