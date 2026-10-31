/// What closing the window does on a desktop.
enum CloseAction {
  /// Hide it and keep running in the tray, so notifications keep arriving.
  hideToTray,

  /// Quit.
  quit,
}

/// The window's close button hides it only when the user wants that and a tray is there to
/// bring it back from. With no tray, hiding would leave the app running with nothing to show,
/// so it quits instead.
CloseAction closeAction({
  required bool closeToTray,
  required bool? trayAvailable,
}) => closeToTray && trayAvailable == true
    ? CloseAction.hideToTray
    : CloseAction.quit;
