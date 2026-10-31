import 'dart:async';
import 'dart:developer' as developer;

import '../data/messages_repository.dart';
import '../data/models.dart';
import '../data/settings_repository.dart';
import '../l10n/app_localizations.dart';
import 'tray_menu.dart';
import 'tray_port.dart';
import 'window_actions.dart';

/// Keeps the tray's menu and tooltip up to date and acts on what the user picks.
class TrayController {
  TrayController({
    required this.port,
    required this.window,
    required this.appName,
    required this.iconAsset,
    required this.iconIsTemplate,
    required this.recent,
    required this.unread,
    required this.prefs,
    required this.setPausedUntil,
    required this.openRoute,
    required this.l10n,
    required this.now,
    this.pauseFor = const Duration(hours: 1),
    this.settle = const Duration(milliseconds: 100),
  });

  final TrayPort port;
  final WindowActions window;
  final String appName;
  final String iconAsset;
  final bool iconIsTemplate;

  /// The latest messages, newest first.
  final Stream<List<InboxItem>> recent;

  /// How many messages are unread, counting only topics that are not muted.
  final Stream<int> unread;
  final Stream<NotificationPrefs> prefs;
  final Future<void> Function(DateTime? until) setPausedUntil;

  /// Opens the thread at an app route.
  final void Function(String route) openRoute;
  final AppLocalizations Function() l10n;
  final DateTime Function() now;
  final Duration pauseFor;

  /// Changes that come close together are shown as one update.
  final Duration settle;

  final List<StreamSubscription<Object?>> _subscriptions = [];
  List<InboxItem> _recent = const [];
  int _unread = 0;
  NotificationPrefs _prefs = const NotificationPrefs();
  Timer? _timer;
  bool _running = false;

  /// Shows the icon and starts following what the menu lists. Throws if the platform cannot
  /// show a tray icon, so the caller can carry on without one.
  Future<void> start() async {
    if (_running) return;
    await port.show(iconAsset, template: iconIsTemplate);
    port.onIconClicked(() => unawaited(window.bringToFront()));
    _running = true;
    _subscriptions
      ..add(
        recent.listen((value) {
          _recent = value;
          _changed();
        }),
      )
      ..add(
        unread.listen((value) {
          _unread = value;
          _changed();
        }),
      )
      ..add(
        prefs.listen((value) {
          _prefs = value;
          _changed();
        }),
      );
    await refresh();
  }

  void _changed() {
    if (!_running) return;
    _timer?.cancel();
    _timer = Timer(settle, () => unawaited(refresh()));
  }

  /// Rebuilds the menu and the tooltip from what is known now.
  Future<void> refresh() async {
    final strings = l10n();
    final paused = _prefs.pausedAt(now());
    try {
      await port.setMenu(
        buildTrayEntries(
          l10n: strings,
          appName: appName,
          recent: _recent,
          paused: paused,
        ),
        (key) => unawaited(_chosen(key)),
      );
      await port.setToolTip(strings.trayTooltipUnread(appName, _unread));
    } on Object catch (error) {
      developer.log('could not update the tray: $error', name: 'tray');
    }
  }

  Future<void> _chosen(String key) async {
    if (key == openKey) {
      await window.bringToFront();
    } else if (key.startsWith(recentPrefix)) {
      openRoute(key.substring(recentPrefix.length));
      await window.bringToFront();
    } else if (key == pauseKey) {
      await setPausedUntil(now().add(pauseFor));
    } else if (key == resumeKey) {
      await setPausedUntil(null);
    } else if (key == quitKey) {
      await window.quit();
    }
  }

  Future<void> stop() async {
    _running = false;
    _timer?.cancel();
    for (final subscription in _subscriptions) {
      await subscription.cancel();
    }
    _subscriptions.clear();
    await port.destroy();
  }
}

/// The messages the tray lists, newest first, from the database.
Stream<List<InboxItem>> recentForTray(MessagesRepository messages) =>
    messages.watchInbox(limit: trayRecentLimit);
