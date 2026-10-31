import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'providers.dart';

/// When the app comes back to the foreground, tries again at once on any server that
/// is waiting to retry, instead of leaving the user to wait out the delay.
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
      onResume: () => ref.read(connectionManagerProvider).reconnectAll(),
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
