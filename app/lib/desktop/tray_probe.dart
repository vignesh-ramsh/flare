import 'dart:developer' as developer;

import 'package:dbus/dbus.dart';

/// Whether the desktop has a tray the app can live in.
abstract interface class TrayProbe {
  Future<bool> available();
}

/// Windows and macOS always have one.
class AlwaysTray implements TrayProbe {
  const AlwaysTray();

  @override
  Future<bool> available() async => true;
}

/// A Linux tray is hosted by the panel, which announces itself on the session bus as the
/// StatusNotifierWatcher. KDE Plasma and most other desktops have one; GNOME has one only with
/// its AppIndicator extension. Without it an icon would never be seen, so the app must not
/// hide into it.
class LinuxTrayProbe implements TrayProbe {
  static const watcher = 'org.kde.StatusNotifierWatcher';

  @override
  Future<bool> available() async {
    final client = DBusClient.session();
    try {
      return (await client.listNames()).contains(watcher);
    } on Object catch (error) {
      developer.log('could not look for a tray: $error', name: 'tray');
      return false;
    } finally {
      await client.close();
    }
  }
}
