import '../loader.dart';
import '../outputs.dart';
import '../spec.dart';

/// A Dart single-quoted string literal for [value].
String dartString(String value) {
  final b = StringBuffer("'");
  for (final rune in value.runes) {
    switch (rune) {
      case 0x27:
        b.write(r"\'");
      case 0x5C:
        b.write(r'\\');
      case 0x24:
        b.write(r'\$');
      case 0x0A:
        b.write(r'\n');
      case 0x0D:
        b.write(r'\r');
      case 0x09:
        b.write(r'\t');
      default:
        if (rune < 0x20 || rune == 0x7F) {
          b.write('\\u{${rune.toRadixString(16)}}');
        } else {
          b.writeCharCode(rune);
        }
    }
  }
  return (b..write("'")).toString();
}

String _argb(int value) => '0x${value.toRadixString(16).toUpperCase()}';

/// `lib/brand/brand.g.dart`: the `Brand` constants the app's Dart code reads,
/// so that no app code contains the name, links or defaults itself.
void emitDart(Brand brand, Outputs out) {
  final s = brand.spec;
  out.text('lib/brand/brand.g.dart', '''
// $generatedNotice
// ignore_for_file: type=lint

/// Everything that identifies this build: its name, look, defaults and links.
///
/// App code must read these constants instead of writing any of them out, so a
/// rebranded build never shows the original name.
abstract final class Brand {
  /// Name shown in the launcher, window title, notifications and About screen.
  static const String name = ${dartString(s.name)};

  /// Name where space is tight (at most 12 characters).
  static const String shortName = ${dartString(s.shortName)};

  /// One-line description of the app.
  static const String description = ${dartString(s.description)};

  /// Scheme of subscribe links: `<urlScheme>://subscribe/<host>/<topic>`.
  static const String urlScheme = ${dartString(s.urlScheme)};

  /// Host for Universal Links and Android App Links, or empty.
  static const String appLinksDomain = ${dartString(s.appLinksDomain)};

  /// Main colour as `0xAARRGGBB`, for `Color(Brand.primaryColor)`.
  static const int primaryColor = ${_argb(s.primaryColor.argb)};

  /// Icon background colour as `0xAARRGGBB`.
  static const int iconBackgroundColor = ${_argb(s.iconBackground.argb)};

  /// Server pre-filled for new users, or empty to make them enter one.
  static const String defaultServerUrl = ${dartString(s.serverUrl)};

  /// Whether users may add servers other than [defaultServerUrl].
  static const bool allowCustomServers = ${s.allowCustomServers};

  /// Push relay this build registers with, or empty if servers push directly.
  static const String pushRelayUrl = ${dartString(s.pushRelayUrl)};

  static const String websiteUrl = ${dartString(s.website)};
  static const String supportUrl = ${dartString(s.support)};
  static const String privacyPolicyUrl = ${dartString(s.privacyPolicy)};
  static const String sourceCodeUrl = ${dartString(s.sourceCode)};

  /// Identity this build registers its push token with, per platform.
  static const String androidApplicationId = ${dartString(s.androidId)};
  static const String appleBundleId = ${dartString(s.appleId)};

  /// Names this app to Windows (its AppUserModelID), so that its notifications
  /// belong to it. Notifications also need a GUID: derive one from this.
  static const String windowsIdentity = ${dartString(s.windowsId)};

  /// App Group shared by the iOS app and its notification extension.
  static const String appleAppGroup = ${dartString(s.appleAppGroup)};

  /// The project this app is built from. Not configurable: every build says
  /// "Based on ..." in its About screen.
  static const String upstreamName = ${dartString(upstreamName)};
  static const String upstreamUrl = ${dartString(upstreamUrl)};
}
''');
}
