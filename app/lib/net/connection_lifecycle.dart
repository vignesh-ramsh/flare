import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../notifications/viewing.dart';
import 'providers.dart';
import 'theme_sync.dart';

/// When the app comes back to the foreground, tries again at once on any server that
/// is waiting to retry, instead of leaving the user to wait out the delay. Also tells
/// notifications whether the window has the user's attention.
class ConnectionLifecycle extends ConsumerStatefulWidget {
  const ConnectionLifecycle({required this.child, super.key});

  final Widget child;

  @override
  ConsumerState<ConnectionLifecycle> createState() =>
      _ConnectionLifecycleState();
}

class _ConnectionLifecycleState extends ConsumerState<ConnectionLifecycle> {
  late final AppLifecycleListener _listener;

  @override
  void initState() {
    super.initState();
    _listener = AppLifecycleListener(
      onResume: () {
        ref.read(connectionManagerProvider).reconnectAll();
        // A server may have added a theme while the app was away.
        unawaited(ref.read(themeSyncProvider).refreshAll());
      },
      // Focused is `resumed`; a notification for the thread on screen is skipped only then.
      onStateChange: (state) => ref
          .read(windowFocusProvider.notifier)
          .set(state == AppLifecycleState.resumed),
    );
  }

  @override
  void dispose() {
    _listener.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
