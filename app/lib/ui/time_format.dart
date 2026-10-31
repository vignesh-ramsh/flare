import 'package:flutter/widgets.dart';
import 'package:intl/intl.dart';

import '../l10n/app_localizations.dart';

/// The time a list row shows: the clock today, "Yesterday", then the date.
String formatListTime(BuildContext context, DateTime time, DateTime now) {
  final locale = Localizations.localeOf(context).toString();
  final day = DateTime(time.year, time.month, time.day);
  final today = DateTime(now.year, now.month, now.day);
  final days = today.difference(day).inDays;
  if (days == 0) return DateFormat.jm(locale).format(time);
  if (days == 1) return AppLocalizations.of(context).yesterday;
  if (time.year == now.year) return DateFormat.MMMd(locale).format(time);
  return DateFormat.yMMMd(locale).format(time);
}

/// The full time, for a message in a thread.
String formatFullTime(BuildContext context, DateTime time) {
  final locale = Localizations.localeOf(context).toString();
  return DateFormat.yMMMd(locale).add_jm().format(time);
}
