import 'dart:ui';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../app/theme.dart';
import '../data/providers.dart';
import '../l10n/app_localizations.dart';
import 'local_notification_sink.dart';
import 'notification_service.dart';
import 'notification_sink.dart';
import 'viewing.dart';

/// Whether this platform shows notifications for a connection the app holds itself: the
/// desktop. (Phones are woken by the system's push service instead.)
bool get holdsOwnConnection =>
    !kIsWeb && isDesktopPlatform(defaultTargetPlatform);

/// The strings, for a notification that is not inside a widget, in the system's language.
AppLocalizations systemStrings() {
  final locale = PlatformDispatcher.instance.locale;
  try {
    return lookupAppLocalizations(locale);
  } on Object {
    return lookupAppLocalizations(const Locale('en'));
  }
}

/// Where notifications are shown. Tests replace it.
final notificationSinkProvider = Provider<NotificationSink>(
  (ref) => LocalNotificationSink(openLabel: systemStrings().notificationOpen),
);

final notificationServiceProvider = Provider<NotificationService>((ref) {
  final topics = ref.watch(topicsRepositoryProvider);
  final service = NotificationService(
    stored: ref.watch(messagesRepositoryProvider).stored,
    topicContext: topics.contextOf,
    prefs: ref.watch(settingsRepositoryProvider).read,
    sink: ref.watch(notificationSinkProvider),
    now: ref.watch(clockProvider),
    l10n: systemStrings,
    viewing: (host, topic) =>
        ref.read(windowFocusProvider) &&
        ref.read(viewedTopicProvider) == (host: host, name: topic),
  );
  ref.onDispose(service.stop);
  return service;
});
