import '../app/routes.dart';
import '../data/models.dart';
import '../l10n/app_localizations.dart';

/// One row of the tray's menu.
class TrayEntry {
  const TrayEntry(this.key, this.label, {this.enabled = true})
    : separator = false;

  const TrayEntry.separator()
    : key = '',
      label = '',
      enabled = false,
      separator = true;

  /// What the controller acts on when the row is chosen.
  final String key;
  final String label;
  final bool enabled;
  final bool separator;

  @override
  bool operator ==(Object other) =>
      other is TrayEntry &&
      other.key == key &&
      other.label == label &&
      other.enabled == enabled &&
      other.separator == separator;

  @override
  int get hashCode => Object.hash(key, label, enabled, separator);

  @override
  String toString() => separator ? '---' : '$key: $label';
}

const openKey = 'open';
const pauseKey = 'pause';
const resumeKey = 'resume';
const quitKey = 'quit';
const recentPrefix = 'recent:';

/// The most recent messages the menu lists.
const trayRecentLimit = 4;

/// A message shortened to fit a menu row.
String _row(InboxItem item) {
  final message = item.message;
  final title = (message.title != null && message.title!.isNotEmpty)
      ? message.title!
      : message.body;
  final text =
      '${message.topic} · ${title.replaceAll(RegExp(r'\s+'), ' ').trim()}';
  return text.length <= 48 ? text : '${text.substring(0, 47)}…';
}

/// The tray's menu: open the app, the latest messages (choosing one opens its thread), pause or
/// resume notifications, quit.
List<TrayEntry> buildTrayEntries({
  required AppLocalizations l10n,
  required String appName,
  required List<InboxItem> recent,
  required bool paused,
}) {
  return [
    TrayEntry(openKey, l10n.trayOpen(appName)),
    if (recent.isNotEmpty) ...[
      const TrayEntry.separator(),
      TrayEntry('header', l10n.trayRecent, enabled: false),
      for (final item in recent.take(trayRecentLimit))
        TrayEntry(
          '$recentPrefix${topicLocation(item.host, item.message.topic)}',
          _row(item),
        ),
    ],
    const TrayEntry.separator(),
    paused
        ? TrayEntry(resumeKey, l10n.trayResume)
        : TrayEntry(pauseKey, l10n.trayPause),
    const TrayEntry.separator(),
    TrayEntry(quitKey, l10n.trayQuit),
  ];
}
