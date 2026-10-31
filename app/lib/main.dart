import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'app/app.dart';
import 'data/messages_repository.dart';
import 'data/providers.dart';
import 'net/connection_lifecycle.dart';
import 'net/providers.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  final container = ProviderContainer();

  // Drop what is past the history the device keeps, then follow every subscription.
  unawaited(
    container
        .read(messagesRepositoryProvider)
        .pruneOlderThan(
          container.read(clockProvider)().subtract(defaultHistory),
        ),
  );
  container.read(connectionManagerProvider).start();

  runApp(
    UncontrolledProviderScope(
      container: container,
      child: const ConnectionLifecycle(child: App()),
    ),
  );
}
