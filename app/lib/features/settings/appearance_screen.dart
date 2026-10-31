import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/theme.dart';
import '../../app/themes/theme_providers.dart';
import '../../app/themes/theme_registry.dart';
import '../../app/themes/theme_resolver.dart';
import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../net/theme_sync.dart';
import 'theme_preview.dart';

/// Settings > Appearance: follow the system's light or dark, or choose one of the themes the
/// app was built with or a server offers. The choice applies at once and is kept.
class AppearanceScreen extends ConsumerStatefulWidget {
  const AppearanceScreen({super.key});

  @override
  ConsumerState<AppearanceScreen> createState() => _AppearanceScreenState();
}

class _AppearanceScreenState extends ConsumerState<AppearanceScreen> {
  static const _system = 'system';

  @override
  void initState() {
    super.initState();
    // A server may have added a theme since the app last asked.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        unawaited(
          ref
              .read(themeSyncProvider)
              .refreshAll(within: const Duration(seconds: 10)),
        );
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final available = ref.watch(availableThemesProvider);
    final chosen = ref.watch(appThemeProvider).active?.ref.id;
    final builtin = [
      for (final t in available)
        if (t.ref.isBuiltin) t,
    ];
    final hosts = {for (final t in available) ?t.ref.serverHost}.toList();

    void choose(String? id) => ref
        .read(settingsRepositoryProvider)
        .setTheme(id == null || id == _system ? null : id);

    Widget section(String title, List<Widget> tiles) => Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 20, 16, 4),
          child: Semantics(
            header: true,
            child: Text(
              title,
              style: theme.textTheme.titleSmall?.copyWith(
                color: theme.colorScheme.primary,
              ),
            ),
          ),
        ),
        ...tiles,
      ],
    );

    return Scaffold(
      appBar: AppBar(
        leading: BackButton(
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/settings'),
        ),
        title: Text(l10n.appearanceTitle),
      ),
      body: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: RadioGroup<String>(
            groupValue: chosen ?? _system,
            onChanged: choose,
            child: ListView(
              padding: const EdgeInsets.only(bottom: 24),
              children: [
                RadioListTile<String>(
                  value: _system,
                  title: Text(l10n.appearanceSystem),
                  subtitle: Text(l10n.appearanceSystemHelp),
                  secondary: _SystemPreview(),
                ),
                if (builtin.isNotEmpty)
                  section(l10n.appearanceBuiltIn, [
                    for (final t in builtin) _ThemeTile(theme: t),
                  ]),
                for (final host in hosts)
                  section(l10n.appearanceFromServer(host), [
                    for (final t in available)
                      if (t.ref.serverHost == host) _ThemeTile(theme: t),
                  ]),
                if (hosts.isEmpty)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 24, 16, 0),
                    child: Text(
                      l10n.appearanceNoMore,
                      style: theme.textTheme.bodyMedium?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// The default theme in both of its brightnesses, side by side.
class _SystemPreview extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        ThemePreview(theme: defaultResolvedTheme(Brightness.light), width: 56),
        const SizedBox(width: 6),
        ThemePreview(theme: defaultResolvedTheme(Brightness.dark), width: 56),
      ],
    );
  }
}

class _ThemeTile extends StatelessWidget {
  const _ThemeTile({required this.theme});

  final AvailableTheme theme;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final kind = theme.spec.brightness == Brightness.dark
        ? l10n.appearanceDark
        : l10n.appearanceLight;
    return RadioListTile<String>(
      value: theme.ref.id,
      title: Text(theme.name),
      subtitle: Text(kind),
      secondary: ThemePreview(theme: resolveTheme(theme.spec)),
    );
  }
}
