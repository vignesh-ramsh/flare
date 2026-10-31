import 'notification_content.dart';

/// Called with the app route of a notification the user tapped.
typedef NotificationTapped = void Function(String route);

/// Where notifications are shown: the platform's notification system. Tests use a fake.
abstract interface class NotificationSink {
  /// Prepares the platform (and, where the system asks, the user's permission).
  Future<void> initialize(NotificationTapped onTap);

  Future<void> show(ShownNotification notification);
}
