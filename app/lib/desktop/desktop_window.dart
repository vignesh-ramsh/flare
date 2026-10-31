import 'package:window_manager/window_manager.dart';

import 'window_actions.dart';

/// The desktop window through `window_manager`.
class DesktopWindowActions implements WindowActions {
  /// Takes over the window's close button, so closing it can hide it to the tray instead of
  /// ending the app. [onClose] is told when the user tries to close the window.
  Future<void> initialize(Future<void> Function() onClose) async {
    await windowManager.ensureInitialized();
    await windowManager.setPreventClose(true);
    windowManager.addListener(_CloseListener(onClose));
  }

  @override
  Future<void> bringToFront() async {
    await windowManager.show();
    await windowManager.focus();
  }

  @override
  Future<void> hide() => windowManager.hide();

  @override
  Future<void> quit() async {
    await windowManager.setPreventClose(false);
    await windowManager.destroy();
  }
}

class _CloseListener with WindowListener {
  _CloseListener(this._onClose);

  final Future<void> Function() _onClose;

  @override
  void onWindowClose() => _onClose();
}
