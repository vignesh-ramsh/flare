import 'package:flutter/material.dart';

import '../app/theme.dart';

/// The unread count, in the brand colour with text chosen to read on it.
class UnreadBadge extends StatelessWidget {
  const UnreadBadge({required this.count, super.key});

  final int count;

  @override
  Widget build(BuildContext context) {
    if (count <= 0) return const SizedBox.shrink();
    return ExcludeSemantics(
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: brandColor,
          borderRadius: BorderRadius.circular(999),
        ),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
          child: Text(
            count > 99 ? '99+' : '$count',
            style: Theme.of(context).textTheme.labelMedium?.copyWith(
              color: onColor(brandColor),
              fontWeight: FontWeight.w700,
            ),
          ),
        ),
      ),
    );
  }
}
