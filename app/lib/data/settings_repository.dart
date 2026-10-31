import '../core/db/database.dart';

/// The user's choices about notifications and the desktop window.
class NotificationPrefs {
  const NotificationPrefs({
    this.enabled = true,
    this.sound = true,
    this.showContent = true,
    this.pausedUntil,
    this.closeToTray = true,
  });

  /// Whether to show notifications at all. Messages are still stored.
  final bool enabled;

  /// Whether a notification may make a sound.
  final bool sound;

  /// Whether a notification shows the message. Off, it only says a message arrived, which
  /// keeps the text out of a lock screen or a screen share.
  final bool showContent;

  /// No notifications until this time, or null. Set from the tray's "Pause".
  final DateTime? pausedUntil;

  /// Whether closing the window leaves the app running in the tray (where there is one).
  final bool closeToTray;

  bool pausedAt(DateTime now) =>
      pausedUntil != null && now.isBefore(pausedUntil!);

  @override
  bool operator ==(Object other) =>
      other is NotificationPrefs &&
      other.enabled == enabled &&
      other.sound == sound &&
      other.showContent == showContent &&
      other.pausedUntil == pausedUntil &&
      other.closeToTray == closeToTray;

  @override
  int get hashCode =>
      Object.hash(enabled, sound, showContent, pausedUntil, closeToTray);
}

const _enabledKey = 'notifications.enabled';
const _soundKey = 'notifications.sound';
const _contentKey = 'notifications.showContent';
const _pausedKey = 'notifications.pausedUntil';
const _closeToTrayKey = 'window.closeToTray';
const _themeKey = 'appearance.theme';

class AppSettingsRepository {
  AppSettingsRepository(this._db);

  final AppDatabase _db;

  Stream<NotificationPrefs> watch() =>
      _db.select(_db.appSettings).watch().map(_parse);

  Future<NotificationPrefs> read() async =>
      _parse(await _db.select(_db.appSettings).get());

  Future<void> setEnabled(bool value) => _set(_enabledKey, '$value');
  Future<void> setSound(bool value) => _set(_soundKey, '$value');
  Future<void> setShowContent(bool value) => _set(_contentKey, '$value');
  Future<void> setCloseToTray(bool value) => _set(_closeToTrayKey, '$value');

  /// Pauses notifications until [time], or resumes them when it is null.
  Future<void> setPausedUntil(DateTime? time) => time == null
      ? (_db.delete(
          _db.appSettings,
        )..where((s) => s.key.equals(_pausedKey))).go()
      : _set(_pausedKey, '${time.millisecondsSinceEpoch}');

  /// Which theme the user chose, as [ThemeRef.id], or null to follow the system's light or dark.
  Stream<String?> watchTheme() => _db
      .select(_db.appSettings)
      .watch()
      .map(
        (rows) => rows
            .where((r) => r.key == _themeKey)
            .map((r) => r.value)
            .firstOrNull,
      );

  /// Chooses a theme by its id, or follows the system when [id] is null.
  Future<void> setTheme(String? id) => id == null
      ? (_db.delete(
          _db.appSettings,
        )..where((s) => s.key.equals(_themeKey))).go()
      : _set(_themeKey, id);

  Future<void> _set(String key, String value) => _db
      .into(_db.appSettings)
      .insertOnConflictUpdate(
        AppSettingsCompanion.insert(key: key, value: value),
      );

  NotificationPrefs _parse(List<SettingRow> rows) {
    final values = {for (final row in rows) row.key: row.value};
    bool flag(String key) =>
        values[key] != 'false'; // a missing setting means on
    final paused = int.tryParse(values[_pausedKey] ?? '');
    return NotificationPrefs(
      enabled: flag(_enabledKey),
      sound: flag(_soundKey),
      showContent: flag(_contentKey),
      closeToTray: flag(_closeToTrayKey),
      pausedUntil: paused == null
          ? null
          : DateTime.fromMillisecondsSinceEpoch(paused),
    );
  }
}
