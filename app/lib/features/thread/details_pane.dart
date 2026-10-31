import 'package:flutter/material.dart';

import '../../data/models.dart';
import '../../l10n/app_localizations.dart';
import 'topic_info_view.dart';

/// The large layout's right-hand pane: facts about the open topic, how to share it and how to
/// leave it.
class DetailsPane extends StatelessWidget {
  const DetailsPane({required this.topic, super.key});

  final TopicRef topic;

  static const double width = 320;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

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
                TopicInfoView(topic: topic),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
