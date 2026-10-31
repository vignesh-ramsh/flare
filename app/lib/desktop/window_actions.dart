import 'package:flutter_riverpod/flutter_riverpod.dart';

/// What the app asks of its window, beyond what Flutter does itself. The real one is for the
/// desktop (hiding to the tray, bringing the window back); everywhere else it does nothing.
abstract interface class WindowActions {
  /// Shows the window and gives it the user's attention, for a tapped notification or the tray.
  Future<void> bringToFront();

  /// Hides the window, leaving the app running in the tray.
  Future<void> hide();

  /// Ends the app.
  Future<void> quit();
}

class NoWindowActions implements WindowActions {
  const NoWindowActions();

  @override
  Future<void> bringToFront() async {}

  @override
  Future<void> hide() async {}

  @override
  Future<void> quit() async {}
}

/// Whether the desktop has a system tray the app can live in: null until known. Where there is
/// none, closing the window quits the app, so it is never left running with nothing to show.
class TrayAvailable extends Notifier<bool?> {
  @override
  bool? build() => null;

  void set(bool available) => state = available;
}

final trayAvailableProvider = NotifierProvider<TrayAvailable, bool?>(
  TrayAvailable.new,
);
