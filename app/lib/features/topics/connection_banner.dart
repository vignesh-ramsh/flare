import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../net/protocol.dart';
import '../../net/providers.dart';
import '../../net/server_status.dart';

/// Tells the user when a server cannot be reached or refused the saved sign-in, with a
/// way to try again at once. Shows nothing while connections are healthy or just
/// starting. With [serverId] it speaks only about that server.
class ConnectionBanner extends ConsumerWidget {
  const ConnectionBanner({this.serverId, super.key});

  final int? serverId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final statuses = ref.watch(connectionStatusProvider).value ?? const {};
    final servers = ref.watch(serversProvider).value ?? const [];
    final hosts = {for (final s in servers) s.id: s.host};

    final troubled = [
      for (final entry in statuses.entries)
        if ((serverId == null || entry.key == serverId) &&
            entry.value.needsAttention &&
            hosts.containsKey(entry.key))
          (entry.key, hosts[entry.key]!, entry.value),
    ];
    if (troubled.isEmpty) return const SizedBox.shrink();

    return Column(
      children: [
        for (final (id, host, status) in troubled)
          _Notice(serverId: id, host: host, status: status),
      ],
    );
  }
}

class _Notice extends ConsumerStatefulWidget {
  const _Notice({
    required this.serverId,
    required this.host,
    required this.status,
  });

  final int serverId;
  final String host;
  final ServerStatus status;

  @override
  ConsumerState<_Notice> createState() => _NoticeState();
}

class _NoticeState extends ConsumerState<_Notice> {
  Timer? _tick;

  @override
  void initState() {
    super.initState();
    // The countdown changes every second.
    _tick = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted && widget.status.state == LinkState.waiting) setState(() {});
    });
  }

  @override
  void dispose() {
    _tick?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final scheme = Theme.of(context).colorScheme;
    final status = widget.status;
    final now = ref.watch(clockProvider)();

    final String message;
    if (status.state == LinkState.authFailed) {
      message = l10n.connectionAuthFailed(widget.host);
    } else {
      final seconds = status.retryAt?.difference(now).inSeconds ?? 0;
      final retry = seconds > 0
          ? l10n.connectionRetryIn(seconds)
          : l10n.connectionRetrying;
      message = '${l10n.connectionUnreachable(widget.host)} $retry';
    }

    return Semantics(
      liveRegion: true,
      container: true,
      child: Material(
        color: scheme.errorContainer,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: Row(
            children: [
              Icon(Icons.cloud_off, size: 20, color: scheme.onErrorContainer),
              const SizedBox(width: 12),
              Expanded(
                child: Text(
                  message,
                  style: Theme.of(context).textTheme.bodyMedium
                      ?.copyWith(color: scheme.onErrorContainer),
                ),
              ),
              // Trying again cannot help when the server refused the saved sign-in.
              if (status.state == LinkState.authFailed)
                TextButton(
                  style: TextButton.styleFrom(
                    foregroundColor: scheme.onErrorContainer,
                  ),
                  onPressed: () => context.push('/signin/${widget.serverId}'),
                  child: Text(l10n.bannerSignIn),
                )
              else
                TextButton(
                  style: TextButton.styleFrom(
                    foregroundColor: scheme.onErrorContainer,
                  ),
                  onPressed: () =>
                      ref.read(connectionManagerProvider).reconnectAll(),
                  child: Text(l10n.connectionTryNow),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Says why a topic is not being followed, when the server refused it.
class TopicRefusedBanner extends ConsumerWidget {
  const TopicRefusedBanner({
    required this.serverId,
    required this.topic,
    super.key,
  });

  final int serverId;
  final String topic;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final code = ref
        .watch(connectionStatusProvider)
        .value?[serverId]
        ?.topics[topic]
        ?.errorCode;
    if (code == null) return const SizedBox.shrink();

    final l10n = AppLocalizations.of(context);
    final scheme = Theme.of(context).colorScheme;
    final host =
        ref
            .watch(serversProvider)
            .value
            ?.where((s) => s.id == serverId)
            .map((s) => s.host)
            .firstOrNull ??
        '';
    final message = switch (code) {
      40101 => l10n.topicNeedsSignIn(host),
      40301 => l10n.topicForbidden,
      _ => l10n.topicRefused(host, code),
    };

    return Semantics(
      liveRegion: true,
      container: true,
      child: Material(
        color: scheme.errorContainer,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          child: Row(
            children: [
              Icon(
                Icons.lock_outline,
                size: 20,
                color: scheme.onErrorContainer,
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Text(
                  message,
                  style: Theme.of(context).textTheme.bodyMedium
                      ?.copyWith(color: scheme.onErrorContainer),
                ),
              ),
              if (code == ErrorCode.unauthorized)
                TextButton(
                  style: TextButton.styleFrom(
                    foregroundColor: scheme.onErrorContainer,
                  ),
                  onPressed: () => context.push('/signin/$serverId'),
                  child: Text(l10n.bannerSignIn),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
