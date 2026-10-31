import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

/// Opens a web address in the user's browser.
abstract interface class LinkOpener {
  /// Returns false when nothing could open it.
  Future<bool> open(Uri uri);
}

class SystemLinkOpener implements LinkOpener {
  const SystemLinkOpener();

  @override
  Future<bool> open(Uri uri) async {
    try {
      return await launchUrl(uri, mode: LaunchMode.externalApplication);
    } on PlatformException {
      return false;
    }
  }
}

/// The address in [text] if it is one the app opens: `http` or `https`, with a host.
///
/// A link in a message is whatever its sender wrote, so a `file:`, `tel:`, `intent:` or private
/// scheme from a stranger is shown as text and never opened.
Uri? webLink(String? text) {
  final value = text?.trim();
  if (value == null || value.isEmpty) return null;
  final uri = Uri.tryParse(value);
  if (uri == null || !uri.hasAuthority || uri.host.isEmpty) return null;
  final scheme = uri.scheme.toLowerCase();
  if (scheme != 'http' && scheme != 'https') return null;
  return uri;
}

final linkOpenerProvider = Provider<LinkOpener>(
  (ref) => const SystemLinkOpener(),
);
