import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../brand/brand.g.dart';
import '../data/providers.dart';
import '../l10n/app_localizations.dart';
import 'router.dart';
import 'themes/theme_providers.dart';

/// Where the app opens. A desktop app started with a subscribe link opens its form; otherwise
/// the topics.
final initialLocationProvider = Provider<String>((ref) => '/');

final routerProvider = Provider<GoRouter>((ref) {
  // The first-run pages show while there is no server.
  final firstRun = FirstRunGate();
  ref.listen(
    serversProvider,
    (previous, next) => firstRun.update(next.value?.isEmpty ?? false),
    fireImmediately: true,
  );
  final router = createRouter(
    initialLocation: ref.read(initialLocationProvider),
    firstRun: firstRun,
  );
  ref.onDispose(() {
    router.dispose();
    firstRun.dispose();
  });
  return router;
});

class App extends ConsumerWidget {
  const App({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final appTheme = ref.watch(appThemeProvider);
    return MaterialApp.router(
      onGenerateTitle: (context) => Brand.name,
      routerConfig: ref.watch(routerProvider),
      theme: appTheme.light,
      darkTheme: appTheme.dark,
      themeMode: appTheme.mode,
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
    );
  }
}
