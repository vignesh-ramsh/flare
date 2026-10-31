import 'package:flutter/material.dart';

import '../app/theme.dart';

/// The unread count, in the theme's accent (the brand colour in the default theme) with text
/// chosen to read on it.
class UnreadBadge extends StatelessWidget {
  const UnreadBadge({required this.count, super.key});

  final int count;

  @override
  Widget build(BuildContext context) {
    if (count <= 0) return const SizedBox.shrink();
    final accent =
        Theme.of(context).extension<PlatformTokens>()?.accent ?? brandColor;
    return ExcludeSemantics(
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: accent,
          borderRadius: BorderRadius.circular(999),
        ),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
          child: Text(
            count > 99 ? '99+' : '$count',
            style: Theme.of(context).textTheme.labelMedium
                ?.copyWith(color: onColor(accent), fontWeight: FontWeight.w700),
          ),
        ),
      ),
    );
  }
}
