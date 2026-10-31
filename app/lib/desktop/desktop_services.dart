import 'dart:developer' as developer;

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../app/app.dart';
import '../brand/brand.g.dart';
import '../data/providers.dart';
import '../notifications/providers.dart';
import 'close_policy.dart';
import 'desktop_window.dart';
import 'tray_controller.dart';
import 'tray_port.dart';
import 'tray_probe.dart';
import 'window_actions.dart';

/// Where the tray's icon is, by platform: a white silhouette on Linux, a template image on
/// macOS (the menu bar tints it), an icon on Windows. `make brand` writes all three.
({String asset, bool template}) trayIconFor(TargetPlatform platform) =>
    switch (platform) {
      TargetPlatform.macOS => (
        asset: 'assets/brand/tray_macos_template.png',
        template: true,
      ),
      TargetPlatform.windows => (
        asset: 'assets/brand/tray_windows.ico',
        template: false,
      ),
      _ => (asset: 'assets/brand/tray_linux.png', template: false),
    };

/// The real window, on the desktop; a window that does nothing everywhere else.
final windowActionsProvider = Provider<WindowActions>(
  (ref) =>
      holdsOwnConnection ? DesktopWindowActions() : const NoWindowActions(),
);

/// How the app finds out whether there is a tray.
final trayProbeProvider = Provider<TrayProbe>(
  (ref) => defaultTargetPlatform == TargetPlatform.linux
      ? LinuxTrayProbe()
      : const AlwaysTray(),
);

final trayPortProvider = Provider<TrayPort>((ref) => PlatformTrayPort());

/// Starts what makes the desktop app live there: the window's close button, and the tray.
/// If the platform cannot give a tray, closing the window quits the app instead of hiding it.
Future<TrayController?> startDesktop(ProviderContainer container) async {
  if (!holdsOwnConnection) return null;
  final window = container.read(windowActionsProvider);

  // Closing the window: hide it where a tray can bring it back, otherwise quit.
  if (window is DesktopWindowActions) {
    try {
      await window.initialize(() async {
        final prefs = await container.read(settingsRepositoryProvider).read();
        final action = closeAction(
          closeToTray: prefs.closeToTray,
          trayAvailable: container.read(trayAvailableProvider),
        );
        await (action == CloseAction.hideToTray
            ? window.hide()
            : window.quit());
      });
    } on Object catch (error) {
      developer.log('could not take over the window: $error', name: 'tray');
    }
  }

  final available = await container.read(trayProbeProvider).available();
  container.read(trayAvailableProvider.notifier).set(available);
  if (!available) return null;

  final icon = trayIconFor(defaultTargetPlatform);
  final controller = TrayController(
    port: container.read(trayPortProvider),
    window: window,
    appName: Brand.name,
    iconAsset: icon.asset,
    iconIsTemplate: icon.template,
    recent: recentForTray(container.read(messagesRepositoryProvider)),
    unread: container
        .read(topicsRepositoryProvider)
        .watchSummaries()
        .map(
          (topics) =>
              topics.fold<int>(0, (sum, t) => sum + (t.muted ? 0 : t.unread)),
        ),
    prefs: container.read(settingsRepositoryProvider).watch(),
    setPausedUntil: container.read(settingsRepositoryProvider).setPausedUntil,
    openRoute: (route) => container.read(routerProvider).go(route),
    l10n: systemStrings,
    now: container.read(clockProvider),
  );
  try {
    await controller.start();
  } on Object catch (error) {
    // No tray after all: closing the window must then quit, not hide.
    developer.log('no tray: $error', name: 'tray');
    container.read(trayAvailableProvider.notifier).set(false);
    return null;
  }
  return controller;
}
