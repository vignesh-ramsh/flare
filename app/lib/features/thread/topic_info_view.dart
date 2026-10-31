import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../../app/theme.dart';
import '../../brand/brand.g.dart';
import '../../data/models.dart';
import '../../data/providers.dart';
import '../../data/server_url.dart';
import '../../data/subscribe_link.dart';
import '../../l10n/app_localizations.dart';
import 'topic_notifications_dialog.dart';

/// Facts about a topic, how to share it, and how to leave it. The large layout shows it in the
/// details pane, and every other layout on the topic's info page.
class TopicInfoView extends ConsumerWidget {
  const TopicInfoView({required this.topic, super.key});

  final TopicRef topic;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final summary = ref.watch(topicSummaryProvider(topic)).value;
    final servers = ref.watch(serversProvider).value ?? const [];
    final server = servers.where((s) => s.host == topic.host).firstOrNull;
    final address = server == null ? null : ServerAddress.parse(server.url);
    // Only a topic the user follows has anything to share.
    final link = address == null || summary == null
        ? null
        : SubscribeLink(server: address, topic: topic.name);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _Fact(label: l10n.detailsTopic, value: topic.name),
        _Fact(label: l10n.detailsServer, value: topic.host),
        if (summary != null) ...[
          _Fact(label: l10n.detailsMessages, value: '${summary.messageCount}'),
          _Fact(
            label: l10n.detailsNotifications,
            value: summary.muted
                ? l10n.notificationsMuted
                : l10n.notificationsOn,
          ),
          Align(
            alignment: AlignmentDirectional.centerStart,
            child: OutlinedButton.icon(
              onPressed: () => TopicNotificationsDialog.show(context, topic),
              icon: const Icon(Icons.notifications_outlined),
              label: Text(l10n.topicNotifications),
            ),
          ),
        ],
        if (link != null) ...[
          const SizedBox(height: 24),
          Text(l10n.shareTitle, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 12),
          _CopyRow(label: l10n.shareLinkLabel, value: link.format()),
          _CopyRow(label: l10n.shareAddressLabel, value: link.webAddress),
          const SizedBox(height: 4),
          _QrCode(link: link),
        ],
        if (summary != null) ...[
          const SizedBox(height: 24),
          Align(
            alignment: AlignmentDirectional.centerStart,
            child: _UnsubscribeButton(topic: summary),
          ),
        ],
      ],
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

/// A value to read or copy, with a button that copies it.
class _CopyRow extends StatelessWidget {
  const _CopyRow({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Expanded(
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
                Text(value, style: theme.textTheme.bodyMedium),
              ],
            ),
          ),
          IconButton(
            tooltip: '${l10n.shareCopy}: $label',
            icon: const Icon(Icons.copy),
            onPressed: () async {
              final messenger = ScaffoldMessenger.maybeOf(context);
              await Clipboard.setData(ClipboardData(text: value));
              messenger?.showSnackBar(
                SnackBar(content: Text(l10n.shareCopied)),
              );
            },
          ),
        ],
      ),
    );
  }
}

/// The subscribe link, its QR code, and what to do with it.
class _QrCode extends StatelessWidget {
  const _QrCode({required this.link});

  final SubscribeLink link;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        LinkQrCode(data: link.format(), label: l10n.shareQrLabel(link.topic)),
        const SizedBox(height: 8),
        Text(
          l10n.shareQrHelp(Brand.name),
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      ],
    );
  }
}

/// A QR code of [data]. Always dark on white with a quiet zone, whatever the theme, because
/// that is what a camera reads.
class LinkQrCode extends StatelessWidget {
  const LinkQrCode({
    required this.data,
    required this.label,
    this.size = 200,
    super.key,
  });

  final String data;

  /// What a screen reader says in place of the picture.
  final String label;
  final double size;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(12),
      child: QrImageView(
        data: data,
        size: size,
        padding: const EdgeInsets.all(12),
        backgroundColor: Colors.white,
        eyeStyle: const QrEyeStyle(
          eyeShape: QrEyeShape.square,
          color: Colors.black,
        ),
        dataModuleStyle: const QrDataModuleStyle(
          dataModuleShape: QrDataModuleShape.square,
          color: Colors.black,
        ),
        semanticsLabel: label,
      ),
    );
  }
}

class _UnsubscribeButton extends ConsumerWidget {
  const _UnsubscribeButton({required this.topic});

  final TopicSummary topic;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final scheme = Theme.of(context).colorScheme;
    return OutlinedButton.icon(
      style: OutlinedButton.styleFrom(
        foregroundColor: scheme.error,
        side: BorderSide(color: scheme.error),
        minimumSize: Size(
          0,
          Theme.of(context).extension<PlatformTokens>()?.targetSize ?? 48,
        ),
      ),
      onPressed: () => _confirm(context, ref),
      icon: const Icon(Icons.notifications_off_outlined),
      label: Text(l10n.unsubscribe),
    );
  }

  Future<void> _confirm(BuildContext context, WidgetRef ref) async {
    final l10n = AppLocalizations.of(context);
    final router = GoRouter.of(context);
    final messenger = ScaffoldMessenger.maybeOf(context);
    final repository = ref.read(topicsRepositoryProvider);

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l10n.unsubscribeTitle(topic.name)),
        content: Text(l10n.unsubscribeBody(topic.messageCount)),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: Text(l10n.cancel),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.error,
              foregroundColor: Theme.of(context).colorScheme.onError,
            ),
            onPressed: () => Navigator.of(context).pop(true),
            child: Text(l10n.unsubscribe),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    // Leave the topic's screens first, so none shows a topic that is about to be gone.
    router.go('/');
    await repository.unsubscribe(topic.serverId, topic.name);
    messenger?.showSnackBar(
      SnackBar(content: Text(l10n.unsubscribed(topic.name))),
    );
  }
}
