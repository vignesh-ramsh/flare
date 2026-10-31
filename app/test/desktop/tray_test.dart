import 'dart:async';

import 'package:app/core/db/database.dart';
import 'package:app/data/models.dart';
import 'package:app/data/settings_repository.dart';
import 'package:app/desktop/close_policy.dart';
import 'package:app/desktop/desktop_services.dart';
import 'package:app/desktop/tray_controller.dart';
import 'package:app/desktop/tray_menu.dart';
import 'package:app/desktop/tray_port.dart';
import 'package:app/desktop/tray_probe.dart';
import 'package:app/desktop/window_actions.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

import '../notifications/fakes.dart';

InboxItem item(
  String topic, {
  String? title,
  String body = 'text',
  String host = 'example.com',
}) => InboxItem(
  host: host,
  message: MessageRow(
    serverId: 1,
    id: 'id-$topic-${title ?? body}',
    topic: topic,
    time: 1,
    title: title,
    body: body,
    priority: 3,
    tags: const [],
  ),
);

class FakePort implements TrayPort {
  String? icon;
  bool? template;
  String? toolTip;
  List<TrayEntry> menu = [];
  void Function(String key)? choose;
  void Function()? click;
  bool destroyed = false;
  Object? failShow;
  int menuUpdates = 0;

  @override
  Future<void> show(String assetPath, {required bool template}) async {
    if (failShow != null) throw failShow!;
    icon = assetPath;
    this.template = template;
  }

  @override
  Future<void> setToolTip(String text) async => toolTip = text;

  @override
  Future<void> setMenu(
    List<TrayEntry> entries,
    void Function(String key) onChosen,
  ) async {
    menu = entries;
    choose = onChosen;
    menuUpdates++;
  }

  @override
  void onIconClicked(void Function() onClicked) => click = onClicked;

  @override
  Future<void> destroy() async => destroyed = true;
}

class FakeWindow implements WindowActions {
  final List<String> calls = [];

  @override
  Future<void> bringToFront() async => calls.add('front');

  @override
  Future<void> hide() async => calls.add('hide');

  @override
  Future<void> quit() async => calls.add('quit');
}

void main() {
  final l10n = english();

  group('what closing the window does', () {
    test('hides it to the tray when the user wants that and there is one', () {
      expect(
        closeAction(closeToTray: true, trayAvailable: true),
        CloseAction.hideToTray,
      );
    });

    test('quits when the user does not want to run in the tray', () {
      expect(
        closeAction(closeToTray: false, trayAvailable: true),
        CloseAction.quit,
      );
    });

    test(
      'quits when there is no tray, so the app is never left running unseen',
      () {
        expect(
          closeAction(closeToTray: true, trayAvailable: false),
          CloseAction.quit,
        );
      },
    );

    test('quits when it is not yet known whether there is a tray', () {
      expect(
        closeAction(closeToTray: true, trayAvailable: null),
        CloseAction.quit,
      );
    });
  });

  group('the tray\'s menu', () {
    List<TrayEntry> entries({
      List<InboxItem> recent = const [],
      bool paused = false,
    }) => buildTrayEntries(
      l10n: l10n,
      appName: 'Acme',
      recent: recent,
      paused: paused,
    );

    test('opens the app, pauses, and quits', () {
      expect(entries().map((e) => e.toString()), [
        'open: Open Acme',
        '---',
        'pause: Pause notifications for 1 hour',
        '---',
        'quit: Quit',
      ]);
    });

    test('offers to resume while paused', () {
      final keys = entries(paused: true).map((e) => e.key);
      expect(keys, contains(resumeKey));
      expect(keys, isNot(contains(pauseKey)));
    });

    test('lists the latest messages, each opening its thread', () {
      final menu = entries(
        recent: [
          item('alerts', title: 'Disk almost full'),
          item(
            'backups',
            body: 'Nightly backup finished',
            host: 'other.example:8443',
          ),
        ],
      );

      expect(menu.map((e) => e.label), contains('Recent'));
      expect(menu.firstWhere((e) => e.label == 'Recent').enabled, isFalse);
      final first = menu.firstWhere(
        (e) => e.label == 'alerts · Disk almost full',
      );
      expect(first.key, 'recent:/topic/example.com/alerts');
      final second = menu.firstWhere(
        (e) => e.label == 'backups · Nightly backup finished',
      );
      expect(second.key, 'recent:/topic/other.example%3A8443/backups');
    });

    test('lists no more than four, and none when there are none', () {
      final many = entries(recent: [for (var i = 0; i < 9; i++) item('t$i')]);
      expect(
        many.where((e) => e.key.startsWith(recentPrefix)),
        hasLength(trayRecentLimit),
      );
      expect(entries().any((e) => e.label == 'Recent'), isFalse);
    });

    test('shortens a long message and puts it on one line', () {
      final long = entries(recent: [item('alerts', title: 'x' * 200)]);
      final label = long
          .firstWhere((e) => e.key.startsWith(recentPrefix))
          .label;
      expect(label.length, 48);
      expect(label, endsWith('…'));

      final lines = entries(recent: [item('alerts', body: 'one\n\n  two')]);
      expect(
        lines.firstWhere((e) => e.key.startsWith(recentPrefix)).label,
        'alerts · one two',
      );
    });
  });

  group('the tray', () {
    late FakePort port;
    late FakeWindow window;
    late StreamController<List<InboxItem>> recent;
    late StreamController<int> unread;
    late StreamController<NotificationPrefs> prefs;
    late List<DateTime?> paused;
    late List<String> opened;
    late TrayController tray;
    final now = DateTime(2025, 1, 15, 12);

    setUp(() {
      port = FakePort();
      window = FakeWindow();
      recent = StreamController.broadcast();
      unread = StreamController.broadcast();
      prefs = StreamController.broadcast();
      paused = [];
      opened = [];
      tray = TrayController(
        port: port,
        window: window,
        appName: 'Acme',
        iconAsset: 'assets/brand/tray_linux.png',
        iconIsTemplate: false,
        recent: recent.stream,
        unread: unread.stream,
        prefs: prefs.stream,
        setPausedUntil: (until) async => paused.add(until),
        openRoute: opened.add,
        l10n: english,
        now: () => now,
        settle: const Duration(milliseconds: 5),
      );
    });

    tearDown(() async {
      await tray.stop();
      await recent.close();
      await unread.close();
      await prefs.close();
    });

    Future<void> settled() =>
        Future<void>.delayed(const Duration(milliseconds: 40));

    test('shows its icon and an initial menu', () async {
      await tray.start();

      expect(port.icon, 'assets/brand/tray_linux.png');
      expect(port.template, isFalse);
      expect(port.menu.first.toString(), 'open: Open Acme');
      expect(port.toolTip, 'Acme: no unread messages');
    });

    test(
      'updates the menu and the tooltip as messages arrive and are read',
      () async {
        await tray.start();
        recent.add([item('alerts', title: 'Disk almost full')]);
        unread.add(3);
        await settled();

        expect(
          port.menu.map((e) => e.label),
          contains('alerts · Disk almost full'),
        );
        expect(port.toolTip, 'Acme: 3 unread messages');

        unread.add(1);
        await settled();
        expect(port.toolTip, 'Acme: 1 unread message');
      },
    );

    test('shows several quick changes as one update', () async {
      await tray.start();
      final before = port.menuUpdates;
      for (var i = 0; i < 10; i++) {
        unread.add(i);
      }
      await settled();
      expect(port.menuUpdates - before, 1);
    });

    test('offers resume once notifications are paused', () async {
      await tray.start();
      prefs.add(
        NotificationPrefs(pausedUntil: now.add(const Duration(minutes: 30))),
      );
      await settled();
      expect(port.menu.map((e) => e.key), contains(resumeKey));

      prefs.add(const NotificationPrefs());
      await settled();
      expect(port.menu.map((e) => e.key), contains(pauseKey));
    });

    test(
      'opens the window from its row and from a click on the icon',
      () async {
        await tray.start();
        port.choose!(openKey);
        port.click!();
        await settled();
        expect(window.calls, ['front', 'front']);
      },
    );

    test('opens a message\'s thread and brings the window forward', () async {
      await tray.start();
      port.choose!('recent:/topic/example.com/alerts');
      await settled();
      expect(opened, ['/topic/example.com/alerts']);
      expect(window.calls, ['front']);
    });

    test('pauses for an hour and resumes', () async {
      await tray.start();
      port.choose!(pauseKey);
      port.choose!(resumeKey);
      await settled();
      expect(paused, [now.add(const Duration(hours: 1)), null]);
    });

    test('quits', () async {
      await tray.start();
      port.choose!(quitKey);
      await settled();
      expect(window.calls, ['quit']);
    });

    test('ignores a row it does not know', () async {
      await tray.start();
      port.choose!('header');
      port.choose!('nonsense');
      await settled();
      expect(window.calls, isEmpty);
      expect(opened, isEmpty);
    });

    test('says so when the platform cannot show an icon, so the caller can do without', () async {
      port.failShow = StateError('no tray');
      await expectLater(tray.start(), throwsStateError);
    });

    test('stops following and removes its icon', () async {
      await tray.start();
      await tray.stop();
      final updates = port.menuUpdates;
      recent.add([item('alerts')]);
      await settled();

      expect(port.destroyed, isTrue);
      expect(port.menuUpdates, updates);
    });

    test('starting twice shows one icon', () async {
      await tray.start();
      await tray.start();
      expect(port.menuUpdates, 1);
    });
  });

  group('the icon', () {
    test('is a template on macOS, an icon on Windows, a PNG elsewhere', () {
      expect(trayIconFor(TargetPlatform.macOS), (
        asset: 'assets/brand/tray_macos_template.png',
        template: true,
      ));
      expect(trayIconFor(TargetPlatform.windows), (
        asset: 'assets/brand/tray_windows.ico',
        template: false,
      ));
      expect(trayIconFor(TargetPlatform.linux), (
        asset: 'assets/brand/tray_linux.png',
        template: false,
      ));
    });
  });

  group('looking for a tray', () {
    test('Windows and macOS always have one', () async {
      expect(await const AlwaysTray().available(), isTrue);
    });

    test('Linux says there is none when no panel hosts one (or there is no session bus)', () async {
      // This machine runs no desktop, so there is no StatusNotifierWatcher to find.
      expect(await LinuxTrayProbe().available(), isFalse);
    });
  });
}
