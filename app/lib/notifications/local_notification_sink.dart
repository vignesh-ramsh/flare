import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:uuid/uuid.dart';

import '../brand/brand.g.dart';
import 'notification_content.dart';
import 'notification_policy.dart';
import 'notification_sink.dart';

/// Shows notifications on Linux (over D-Bus, to whatever notification daemon the desktop
/// runs), macOS (Notification Center) and Windows (toasts), with `flutter_local_notifications`.
class LocalNotificationSink implements NotificationSink {
  LocalNotificationSink({
    required this.openLabel,
    FlutterLocalNotificationsPlugin? plugin,
  }) : _plugin = plugin ?? FlutterLocalNotificationsPlugin();

  /// The default action's name on Linux, such as "Open".
  final String openLabel;
  final FlutterLocalNotificationsPlugin _plugin;

  @override
  Future<void> initialize(NotificationTapped onTap) async {
    await _plugin.initialize(
      settings: InitializationSettings(
        linux: LinuxInitializationSettings(
          defaultActionName: openLabel,
          defaultIcon: AssetsLinuxIcon('assets/brand/logo.png'),
        ),
        macOS: const DarwinInitializationSettings(),
        windows: WindowsInitializationSettings(
          appName: Brand.name,
          appUserModelId: Brand.windowsIdentity,
          // Windows wants a GUID for the app; one derived from the brand's identity is the same
          // on every run and different for every brand.
          guid: const Uuid().v5(
            Namespace.url.value,
            'notifications:${Brand.windowsIdentity}',
          ),
        ),
      ),
      onDidReceiveNotificationResponse: (response) {
        final route = response.payload;
        if (route != null && route.isNotEmpty) onTap(route);
      },
    );
  }

  @override
  Future<void> show(ShownNotification n) {
    final persistent = n.level == NotificationLevel.persistent;
    final quiet = n.level == NotificationLevel.quiet;
    // Linux and Windows have no place for a subtitle, so it goes above the text.
    final plainBody = [
      if (n.subtitle != null) n.subtitle!,
      if (n.body.isNotEmpty) n.body,
    ].join('\n');

    return _plugin.show(
      id: n.id,
      title: n.title,
      body: plainBody,
      payload: n.route,
      notificationDetails: NotificationDetails(
        linux: LinuxNotificationDetails(
          // Critical notifications are not removed by the timeout on most desktops.
          urgency: persistent
              ? LinuxNotificationUrgency.critical
              : quiet
              ? LinuxNotificationUrgency.low
              : LinuxNotificationUrgency.normal,
          timeout: persistent
              ? const LinuxNotificationTimeout.expiresNever()
              : const LinuxNotificationTimeout.systemDefault(),
          suppressSound: !n.sound,
          category: LinuxNotificationCategory.imReceived,
        ),
        macOS: DarwinNotificationDetails(
          subtitle: n.subtitle,
          presentSound: n.sound,
          threadIdentifier: n.route,
          interruptionLevel: persistent
              ? InterruptionLevel.timeSensitive
              : quiet
              ? InterruptionLevel.passive
              : InterruptionLevel.active,
        ),
        windows: WindowsNotificationDetails(
          audio: n.sound
              ? WindowsNotificationAudio.preset(
                  sound: WindowsNotificationSound.defaultSound,
                )
              : WindowsNotificationAudio.silent(),
          duration: persistent
              ? WindowsNotificationDuration.long
              : WindowsNotificationDuration.short,
        ),
      ),
    );
  }
}
