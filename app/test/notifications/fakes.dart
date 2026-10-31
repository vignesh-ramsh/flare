import 'package:app/l10n/app_localizations.dart';
import 'package:app/notifications/notification_content.dart';
import 'package:app/notifications/notification_sink.dart';
import 'package:flutter/widgets.dart';

/// A sink that records what it was asked to show.
class RecordingSink implements NotificationSink {
  final List<ShownNotification> shown = [];
  NotificationTapped? onTap;
  bool initialized = false;

  /// Throw on the show call with this index (0 based), to see that later ones still happen.
  int? failOn;
  int _calls = 0;

  @override
  Future<void> initialize(NotificationTapped onTap) async {
    initialized = true;
    this.onTap = onTap;
  }

  @override
  Future<void> show(ShownNotification notification) async {
    final call = _calls++;
    if (call == failOn) throw StateError('the notification service is gone');
    shown.add(notification);
  }
}

AppLocalizations english() => lookupAppLocalizations(const Locale('en'));
