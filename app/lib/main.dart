import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'app/app.dart';
import 'data/startup.dart';
import 'net/connection_lifecycle.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final container = ProviderContainer();
  await startServices(container);

  runApp(
    UncontrolledProviderScope(
      container: container,
      child: const ConnectionLifecycle(child: App()),
    ),
  );
}
