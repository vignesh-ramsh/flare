import 'package:flutter/material.dart';

import '../l10n/app_localizations.dart';

/// Priority is shown with a word and a glyph, never colour alone. The default
/// priority (3) shows nothing.
String? priorityWord(AppLocalizations l10n, int priority) => switch (priority) {
  1 => l10n.priorityMin,
  2 => l10n.priorityLow,
  4 => l10n.priorityHigh,
  5 => l10n.priorityUrgent,
  _ => null,
};

IconData? priorityIcon(int priority) => switch (priority) {
  1 => Icons.keyboard_double_arrow_down,
  2 => Icons.keyboard_arrow_down,
  4 => Icons.priority_high,
  5 => Icons.notification_important,
  _ => null,
};

class PriorityBadge extends StatelessWidget {
  const PriorityBadge({required this.priority, super.key});

  final int priority;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final word = priorityWord(l10n, priority);
    final icon = priorityIcon(priority);
    if (word == null || icon == null) return const SizedBox.shrink();

    final scheme = Theme.of(context).colorScheme;
    final (background, foreground) = switch (priority) {
      5 => (scheme.errorContainer, scheme.onErrorContainer),
      4 => (scheme.tertiaryContainer, scheme.onTertiaryContainer),
      _ => (scheme.surfaceContainerHighest, scheme.onSurfaceVariant),
    };

    return Semantics(
      label: l10n.priorityLabel(word),
      excludeSemantics: true,
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: background,
          borderRadius: BorderRadius.circular(8),
        ),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 16, color: foreground),
              const SizedBox(width: 4),
              Text(
                word,
                style: Theme.of(context).textTheme.labelMedium
                    ?.copyWith(color: foreground, fontWeight: FontWeight.w600),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
