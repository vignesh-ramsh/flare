import 'package:flutter/foundation.dart';

import 'theme_resolver.dart';
import 'theme_spec.dart';

/// Which theme: one built into the app (from the brand folder), or one a server offers.
@immutable
class ThemeRef {
  const ThemeRef.builtin(this.name) : serverHost = null;
  const ThemeRef.server(String this.serverHost, this.name);

  /// The server that offers the theme, or null for a built-in one.
  final String? serverHost;
  final String name;

  bool get isBuiltin => serverHost == null;

  /// What is saved as the user's choice: `builtin:<name>` or `server:<host>:<name>`, the name in
  /// lower case so a change of capital letters does not lose the choice.
  String get id => isBuiltin
      ? 'builtin:${name.toLowerCase()}'
      : 'server:$serverHost:${name.toLowerCase()}';

  /// Reads an id saved by [id]; null if it is not one.
  static ThemeRef? parse(String? id) {
    if (id == null) return null;
    if (id.startsWith('builtin:')) {
      final name = id.substring('builtin:'.length);
      return name.isEmpty ? null : ThemeRef.builtin(name);
    }
    if (id.startsWith('server:')) {
      final rest = id.substring('server:'.length);
      // The name has no colon; a host may (a port), so split at the last one.
      final cut = rest.lastIndexOf(':');
      if (cut <= 0 || cut == rest.length - 1) return null;
      return ThemeRef.server(rest.substring(0, cut), rest.substring(cut + 1));
    }
    return null;
  }

  @override
  bool operator ==(Object other) => other is ThemeRef && other.id == id;

  @override
  int get hashCode => id.hashCode;

  @override
  String toString() => id;
}

/// A theme the user can choose, and where it came from.
@immutable
class AvailableTheme {
  const AvailableTheme({required this.ref, required this.spec});

  final ThemeRef ref;
  final ThemeSpec spec;

  String get name => spec.name;
}

/// Whether [spec] may be offered: its colours (set or derived) keep text readable. A server
/// can send anything, and a theme nobody can read could not be switched away from.
bool isReadable(ThemeSpec spec) =>
    resolveTheme(spec).readabilityProblems().isEmpty;

/// The themes to offer: those built in, then each server's, with unreadable ones and repeated
/// names (within a source, compared without regard to case) left out. The first of a repeated
/// name wins.
List<AvailableTheme> collectThemes({
  required Iterable<ThemeSpec> builtin,
  Map<String, Iterable<ThemeSpec>> servers = const {},
}) {
  final out = <AvailableTheme>[];
  void add(ThemeRef Function(String name) ref, Iterable<ThemeSpec> specs) {
    final seen = <String>{};
    for (final spec in specs) {
      if (!seen.add(spec.nameKey) || !isReadable(spec)) continue;
      out.add(AvailableTheme(ref: ref(spec.name), spec: spec));
    }
  }

  add(ThemeRef.builtin, builtin);
  for (final entry in servers.entries) {
    add((name) => ThemeRef.server(entry.key, name), entry.value);
  }
  return out;
}
