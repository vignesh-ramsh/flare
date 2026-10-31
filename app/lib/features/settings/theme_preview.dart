import 'package:flutter/material.dart';

import '../../app/themes/theme_resolver.dart';

/// A small picture of a theme: its page, a card on it with two lines of text, and a button.
/// Decoration only, so a screen reader skips it and reads the name next to it instead.
class ThemePreview extends StatelessWidget {
  const ThemePreview({
    required this.theme,
    this.width = 84,
    this.height = 56,
    super.key,
  });

  final ResolvedTheme theme;
  final double width;
  final double height;

  @override
  Widget build(BuildContext context) {
    final scheme = theme.scheme;
    return ExcludeSemantics(
      child: SizedBox(
        width: width,
        height: height,
        child: DecoratedBox(
          decoration: BoxDecoration(
            color: theme.background,
            border: Border.all(color: scheme.outline),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Padding(
            padding: const EdgeInsets.all(6),
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: scheme.surface,
                border: Border.all(color: scheme.outlineVariant),
                borderRadius: BorderRadius.circular(5),
              ),
              child: Padding(
                padding: const EdgeInsets.all(4),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    _bar(scheme.onSurface, width * 0.42),
                    const SizedBox(height: 3),
                    _bar(scheme.onSurfaceVariant, width * 0.28),
                    const Spacer(),
                    Align(
                      alignment: Alignment.centerRight,
                      child: _bar(scheme.primary, width * 0.22, height: 8),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _bar(Color color, double width, {double height = 4}) => SizedBox(
    width: width,
    height: height,
    child: DecoratedBox(
      decoration: BoxDecoration(
        color: color,
        borderRadius: BorderRadius.circular(height),
      ),
    ),
  );
}
