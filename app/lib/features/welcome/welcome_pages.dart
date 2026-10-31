import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/window_class.dart';
import '../../brand/brand.g.dart';
import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../ui/brand_logo.dart';
import '../servers/server_check_form.dart';
import '../servers/sign_in_form.dart';

/// A first-run page: the whole screen on a phone, a centred card on a wide window.
class WelcomeScaffold extends StatelessWidget {
  const WelcomeScaffold({required this.children, this.onBack, super.key});

  final List<Widget> children;
  final VoidCallback? onBack;

  @override
  Widget build(BuildContext context) {
    final content = Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: children,
      ),
    );
    final wide = context.windowClass != WindowClass.compact;

    return Scaffold(
      appBar: onBack == null
          ? null
          : AppBar(
              automaticallyImplyLeading: false,
              leading: BackButton(onPressed: onBack),
            ),
      body: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 480),
              child: wide ? Card(child: content) : content,
            ),
          ),
        ),
      ),
    );
  }
}

/// `/welcome`: what the app is.
class WelcomePage extends StatelessWidget {
  const WelcomePage({super.key});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    return WelcomeScaffold(
      children: [
        const Center(child: BrandLogo(size: 72)),
        const SizedBox(height: 24),
        Text(
          l10n.welcomeTitle(Brand.name),
          style: theme.textTheme.headlineSmall,
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: 8),
        Text(
          Brand.description,
          style: theme.textTheme.bodyLarge?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
          textAlign: TextAlign.center,
        ),
        const SizedBox(height: 32),
        FilledButton(
          onPressed: () => context.go('/welcome/server'),
          child: Text(l10n.welcomeGetStarted),
        ),
      ],
    );
  }
}

/// `/welcome/server`: choose a server.
class WelcomeServerPage extends StatelessWidget {
  const WelcomeServerPage({super.key});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    return WelcomeScaffold(
      onBack: () => context.go('/welcome'),
      children: [
        Text(l10n.welcomeServerTitle, style: theme.textTheme.headlineSmall),
        const SizedBox(height: 8),
        Text(
          l10n.welcomeServerBody,
          style: theme.textTheme.bodyLarge?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
        const SizedBox(height: 24),
        ServerCheckForm(
          onAdded: (server, info) =>
              context.go('/welcome/account?server=${server.id}'),
        ),
      ],
    );
  }
}

/// `/welcome/account?server=ID`: sign in, or skip.
class WelcomeAccountPage extends ConsumerWidget {
  const WelcomeAccountPage({required this.serverId, super.key});

  final int serverId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final server = ref.watch(serverProvider(serverId));

    return server.when(
      loading: () => const Scaffold(),
      error: (error, stack) => Scaffold(body: Center(child: Text('$error'))),
      data: (row) {
        if (row == null) {
          // The server was removed under us: start again.
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (context.mounted) context.go('/welcome/server');
          });
          return const Scaffold();
        }
        return WelcomeScaffold(
          onBack: () => context.go('/welcome/server'),
          children: [
            Text(
              l10n.signInTitle(row.host),
              style: theme.textTheme.headlineSmall,
            ),
            const SizedBox(height: 8),
            Text(
              l10n.welcomeAccountBody,
              style: theme.textTheme.bodyLarge?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 24),
            SignInForm(
              server: row,
              onSignedIn: () => context.go('/'),
              onSkip: () => context.go('/'),
            ),
          ],
        );
      },
    );
  }
}
