import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../brand/brand.g.dart';
import '../../l10n/app_localizations.dart';
import '../../ui/brand_logo.dart';
import '../../ui/link_opener.dart';

/// Who made this build, and what it is based on. Every build says "Based on ...".
class AboutScreen extends ConsumerWidget {
  const AboutScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    final links = <(String, String)>[
      (l10n.aboutWebsite, Brand.websiteUrl),
      (l10n.aboutSupport, Brand.supportUrl),
      (l10n.aboutSourceCode, Brand.sourceCodeUrl),
      (l10n.aboutPrivacyPolicy, Brand.privacyPolicyUrl),
      (l10n.aboutUpstream, Brand.upstreamUrl),
    ].where((link) => link.$2.isNotEmpty).toList();

    return Scaffold(
      appBar: AppBar(
        leading: BackButton(
          onPressed: () =>
              context.canPop() ? context.pop() : context.go('/settings'),
        ),
        title: Text(l10n.aboutTitle(Brand.name)),
      ),
      body: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: ListView(
            padding: const EdgeInsets.symmetric(vertical: 24),
            children: [
              const Center(child: BrandLogo(size: 72)),
              const SizedBox(height: 16),
              Text(
                Brand.name,
                style: theme.textTheme.headlineSmall,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 4),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 24),
                child: Text(
                  Brand.description,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                  textAlign: TextAlign.center,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                l10n.aboutBasedOn(Brand.upstreamName),
                style: theme.textTheme.bodyMedium,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 16),
              for (final (label, url) in links)
                ListTile(
                  title: Text(label),
                  subtitle: Text(url),
                  trailing: webLink(url) == null
                      ? null
                      : const Icon(Icons.open_in_new),
                  onTap: webLink(url) == null
                      ? null
                      : () async {
                          final messenger = ScaffoldMessenger.of(context);
                          final failed = l10n.messageLinkOpenFailed;
                          final opened = await ref
                              .read(linkOpenerProvider)
                              .open(webLink(url)!);
                          if (!opened) {
                            messenger.showSnackBar(
                              SnackBar(content: Text(failed)),
                            );
                          }
                        },
                ),
            ],
          ),
        ),
      ),
    );
  }
}
