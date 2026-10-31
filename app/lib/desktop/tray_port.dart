import 'package:tray_manager/tray_manager.dart';

import 'tray_menu.dart';

/// The tray, as the app needs it. One implementation talks to the platform; tests use a fake.
abstract interface class TrayPort {
  /// Shows the icon from the Flutter asset at [assetPath].
  Future<void> show(String assetPath, {required bool template});

  Future<void> setToolTip(String text);

  /// Replaces the menu. [onChosen] is called with the key of the row the user picks.
  Future<void> setMenu(
    List<TrayEntry> entries,
    void Function(String key) onChosen,
  );

  /// [onClicked] is called when the icon is clicked, where the platform reports it.
  void onIconClicked(void Function() onClicked);

  Future<void> destroy();
}

/// The tray through `tray_manager`: the menu bar on macOS, the notification area on Windows, a
/// StatusNotifierItem on Linux.
class PlatformTrayPort implements TrayPort {
  TrayIcon? _icon;

  TrayIcon get _trayIcon => _icon ??= TrayIcon.create()!;

  @override
  Future<void> show(String assetPath, {required bool template}) async {
    final icon = ImageAsset.fromAsset(assetPath);
    if (icon == null) {
      throw ArgumentError.value(
        assetPath,
        'assetPath',
        'Unable to load the tray icon',
      );
    }
    _trayIcon
      ..isIconTemplate = template
      ..icon = icon
      ..setVisible(true);
  }

  @override
  Future<void> setToolTip(String text) async => _trayIcon.setTooltip(text);

  @override
  Future<void> setMenu(
    List<TrayEntry> entries,
    void Function(String key) onChosen,
  ) async {
    final menu = Menu.create()!;
    for (final entry in entries) {
      if (entry.separator) {
        menu.addSeparator();
        continue;
      }
      final item = MenuItem.createWithLabelAndType(
        entry.label,
        MenuItemType.normal,
      )!;
      item.isEnabled = entry.enabled;
      item.addListener((event) {
        if (event is MenuItemClickedEvent) onChosen(entry.key);
      });
      menu.addItem(item);
    }
    _trayIcon.setContextMenu(menu);
  }

  @override
  void onIconClicked(void Function() onClicked) {
    _trayIcon.addListener((event) {
      if (event is TrayIconClickedEvent) onClicked();
    });
  }

  @override
  Future<void> destroy() async {
    _trayIcon.setVisible(false);
    _icon = null;
  }
}
