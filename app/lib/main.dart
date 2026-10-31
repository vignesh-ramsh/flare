import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'app/app.dart';
import 'app/deep_link.dart';
import 'data/startup.dart';
import 'net/connection_lifecycle.dart';

/// [arguments] are the program's command line. On a desktop the system starts the app with a
/// subscribe link as the argument once the link's scheme is registered to it.
Future<void> main([List<String> arguments = const []]) async {
  WidgetsFlutterBinding.ensureInitialized();
  final link = routeFromArguments(arguments);
  final container = ProviderContainer(
    overrides: [
      if (link != null) initialLocationProvider.overrideWithValue(link),
    ],
  );
  await startServices(container);

  runApp(
    UncontrolledProviderScope(
      container: container,
      child: const ConnectionLifecycle(child: App()),
    ),
  );
}
