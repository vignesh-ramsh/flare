import 'package:yaml/yaml.dart';

import 'raster.dart';

/// The upstream project's name and home. Shown as "Based on ..." in every
/// build's About screen; deliberately not configurable (see TRADEMARKS.md).
const upstreamName = 'Flare';
const upstreamUrl = 'https://github.com/vignesh-ramsh/flare';

const _schemaVersion = 1;

/// Problems found while loading a brand folder.
final class Report {
  final List<String> errors = [];
  final List<String> warnings = [];

  bool get hasErrors => errors.isNotEmpty;

  void error(String where, String message) => errors.add('$where: $message');
  void warn(String where, String message) => warnings.add('$where: $message');
}

/// The validated contents of `brand.yaml`.
final class BrandSpec {
  BrandSpec({
    required this.name,
    required this.shortName,
    required this.description,
    required this.urlScheme,
    required this.appLinksDomain,
    required this.androidId,
    required this.appleId,
    required this.appleTeamId,
    required this.windowsId,
    required this.windowsPublisher,
    required this.linuxId,
    required this.iconFile,
    required this.symbolFile,
    required this.iconBackground,
    required this.primaryColor,
    required this.serverUrl,
    required this.allowCustomServers,
    required this.pushRelayUrl,
    required this.website,
    required this.support,
    required this.privacyPolicy,
    required this.sourceCode,
    required this.firebaseAndroidConfig,
  });

  final String name;
  final String shortName;
  final String description;
  final String urlScheme;
  final String appLinksDomain;

  final String androidId;
  final String appleId;
  final String appleTeamId;
  final String windowsId;
  final String windowsPublisher;
  final String linuxId;

  /// Paths relative to the brand folder.
  final String iconFile;
  final String symbolFile;

  final Rgb iconBackground;
  final Rgb primaryColor;

  final String serverUrl;
  final bool allowCustomServers;
  final String pushRelayUrl;

  final String website;
  final String support;
  final String privacyPolicy;
  final String sourceCode;

  /// Path relative to the brand folder, or empty.
  final String firebaseAndroidConfig;

  /// Executable name on Windows and Linux: the last part of the Linux ID.
  String get binaryName => linuxId.split('.').last.toLowerCase();

  /// Bundle ID of the iOS Notification Service Extension.
  String get appleExtensionId => '$appleId.NotificationService';

  /// App Group shared by the iOS app and its extension.
  String get appleAppGroup => 'group.$appleId';
}

// --- Rules -----------------------------------------------------------------

/// Letters, digits, spaces and a little punctuation: safe in file names,
/// Info.plist, Windows resources and `.xcconfig` files.
final _nameChars = RegExp(
  r"^[\p{L}\p{N}][\p{L}\p{N} .,'&+!_-]*$",
  unicode: true,
);
final _controlChars = RegExp(r'\p{Cc}', unicode: true);
final _unsafeInBuildFiles = RegExp(r'[\\"$;]');
final _scheme = RegExp(r'^[a-z][a-z0-9+.-]{1,31}$');
final _domain = RegExp(
  r'^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$',
);
final _androidId = RegExp(r'^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$');
final _appleId = RegExp(r'^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$');
final _teamId = RegExp(r'^[A-Z0-9]{10}$');
final _windowsId = RegExp(r'^[A-Za-z0-9][A-Za-z0-9.-]{1,48}[A-Za-z0-9]$');
final _linuxId = RegExp(
  r'^[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z_][A-Za-z0-9_-]*)+$',
);
final _publisher = RegExp(r'^[A-Za-z][A-Za-z0-9.]*=.+$');
final _color = RegExp(r'^#[0-9a-fA-F]{6}$');

/// Schemes an app must not claim: they belong to the OS, browsers or other
/// software.
const _reservedSchemes = {
  'http',
  'https',
  'file',
  'ftp',
  'ws',
  'wss',
  'data',
  'javascript',
  'about',
  'mailto',
  'tel',
  'sms',
  'content',
  'intent',
  'market',
  'blob',
  'chrome',
  'view-source',
  'ssh',
  'git',
};

int _length(String s) => s.runes.length;

bool _isLoopback(String host) =>
    host == 'localhost' ||
    host == '127.0.0.1' ||
    host == '::1' ||
    host == '10.0.2.2';

/// Reads one level of a YAML mapping, reporting wrong types and unknown keys
/// with the full dotted path of the offending field.
final class _Section {
  _Section(this._report, this._path, this._map);

  final Report _report;
  final String _path;
  final Map<String, Object?> _map;
  final Set<String> _used = {};

  String at(String key) => _path.isEmpty ? key : '$_path.$key';

  /// A trimmed string, or null if the field is missing or invalid (after
  /// reporting it). Optional fields that are missing or empty return ''.
  String? string(String key, {bool required = true}) {
    _used.add(key);
    final value = _map[key];
    if (value == null) {
      if (required) {
        _report.error(
          at(key),
          'is required (a value starting with # must be quoted)',
        );
        return null;
      }
      return '';
    }
    if (value is! String) {
      _report.error(
        at(key),
        'must be a string; put it in quotes (found $value)',
      );
      return null;
    }
    final trimmed = value.trim();
    if (required && trimmed.isEmpty) {
      _report.error(at(key), 'must not be empty');
      return null;
    }
    return trimmed;
  }

  bool? boolean(String key) {
    _used.add(key);
    final value = _map[key];
    if (value is bool) return value;
    _report.error(
      at(key),
      value == null
          ? 'is required (true or false)'
          : 'must be true or false (found $value)',
    );
    return null;
  }

  int? integer(String key) {
    _used.add(key);
    final value = _map[key];
    if (value is int) return value;
    _report.error(
      at(key),
      value == null ? 'is required' : 'must be a number (found $value)',
    );
    return null;
  }

  _Section section(String key, {bool required = true}) {
    _used.add(key);
    final value = _map[key];
    if (value is Map) return _Section(_report, at(key), _stringKeys(value));
    if (value != null || required) {
      _report.error(
        at(key),
        value == null
            ? 'section is missing'
            : 'must be a section of keys (found $value)',
      );
    }
    return _Section(_report, at(key), const {});
  }

  /// Reports every key that no read asked for.
  void finish() {
    for (final key in _map.keys) {
      if (!_used.contains(key)) {
        final known = (_used.toList()..sort()).join(', ');
        _report.error(at(key), 'unknown key (expected one of: $known)');
      }
    }
  }
}

Map<String, Object?> _stringKeys(Map<Object?, Object?> map) => {
  for (final e in map.entries) '${e.key}': e.value,
};

/// Parses and validates the text of a `brand.yaml`. Returns null if anything is
/// wrong; every problem found is in [report].
BrandSpec? parseBrandYaml(String text, Report report) {
  final Object? root;
  try {
    root = loadYaml(text);
  } on YamlException catch (e) {
    report.error('brand.yaml', e.message);
    return null;
  }
  if (root is! Map) {
    report.error('brand.yaml', 'must contain a mapping of settings');
    return null;
  }

  final top = _Section(report, '', _stringKeys(root));
  final errorsBefore = report.errors.length;

  // --- schema
  final schema = top.integer('schema');
  if (schema != null && schema != _schemaVersion) {
    report.error(
      'schema',
      'is $schema, but this tool understands schema $_schemaVersion',
    );
  }

  // --- app
  final app = top.section('app');
  final name = app.string('name');
  final shortNameRaw = app.string('short_name', required: false);
  final description = app.string('description');
  final urlScheme = app.string('url_scheme');
  final appLinksDomain = app.string('app_links_domain', required: false);
  app.finish();

  if (name != null) {
    if (_length(name) > 30) {
      report.error(
        'app.name',
        'is ${_length(name)} characters; the stores allow at most 30',
      );
    }
    if (!_nameChars.hasMatch(name)) {
      report.error(
        'app.name',
        'may only contain letters, digits, spaces and . , \' & + ! _ - (and must start with a letter or digit)',
      );
    }
  }
  final shortName = (shortNameRaw == null || shortNameRaw.isEmpty)
      ? name
      : shortNameRaw;
  if (shortName != null) {
    if (_length(shortName) > 12) {
      report.error(
        'app.short_name',
        _length(shortName) == _length(name ?? '')
            ? 'is not set and app.name is longer than 12 characters; set a short_name of at most 12'
            : 'is ${_length(shortName)} characters; the limit is 12 (it is the Android launcher label)',
      );
    } else if (!_nameChars.hasMatch(shortName)) {
      report.error(
        'app.short_name',
        'may only contain letters, digits, spaces and . , \' & + ! _ -',
      );
    }
  }
  if (description != null) {
    if (_length(description) > 80) {
      report.error(
        'app.description',
        'is ${_length(description)} characters; Google Play allows at most 80',
      );
    }
    if (_controlChars.hasMatch(description)) {
      report.error('app.description', 'must be a single line');
    }
    if (_unsafeInBuildFiles.hasMatch(description)) {
      report.error(
        'app.description',
        r'must not contain \ " $ or ; (they would break the generated build files)',
      );
    }
  }
  if (urlScheme != null) {
    if (!_scheme.hasMatch(urlScheme)) {
      report.error(
        'app.url_scheme',
        'must be 2-32 lowercase letters, digits, + . - and start with a letter (found "$urlScheme")',
      );
    } else if (_reservedSchemes.contains(urlScheme)) {
      report.error(
        'app.url_scheme',
        '"$urlScheme" belongs to the operating system or browsers; pick your own',
      );
    }
  }
  if (appLinksDomain != null &&
      appLinksDomain.isNotEmpty &&
      !_domain.hasMatch(appLinksDomain)) {
    report.error(
      'app.app_links_domain',
      'must be a host name like links.example.org, without https:// or a path',
    );
  }

  // --- ids
  final ids = top.section('ids');
  final androidId = ids.string('android');
  final appleId = ids.string('apple');
  final appleTeamId = ids.string('apple_team_id', required: false);
  final windowsId = ids.string('windows');
  final windowsPublisher = ids.string('windows_publisher');
  final linuxId = ids.string('linux');
  ids.finish();

  if (androidId != null && !_androidId.hasMatch(androidId)) {
    report.error(
      'ids.android',
      'must look like com.company.app: at least two parts, each starting with a letter, using letters, digits and _ (found "$androidId")',
    );
  }
  if (appleId != null) {
    if (!_appleId.hasMatch(appleId)) {
      report.error(
        'ids.apple',
        'must look like com.company.app, using letters, digits, - and . (found "$appleId")',
      );
    } else if (appleId.length + '.NotificationService'.length > 155) {
      report.error(
        'ids.apple',
        'is too long: Apple allows 155 characters, and ".NotificationService" is added for the iOS extension',
      );
    }
  }
  if (appleTeamId != null &&
      appleTeamId.isNotEmpty &&
      !_teamId.hasMatch(appleTeamId)) {
    report.error(
      'ids.apple_team_id',
      'must be the 10 letters and digits of your Apple Developer Team ID (found "$appleTeamId")',
    );
  }
  if (windowsId != null && !_windowsId.hasMatch(windowsId)) {
    report.error(
      'ids.windows',
      'must be 3-50 letters, digits, . or - for the MSIX identity, like Company.App (found "$windowsId")',
    );
  }
  if (windowsPublisher != null) {
    if (!_publisher.hasMatch(windowsPublisher)) {
      report.error(
        'ids.windows_publisher',
        'must be the subject of your signing certificate, like "CN=Company" (found "$windowsPublisher")',
      );
    } else if (_unsafeInBuildFiles.hasMatch(windowsPublisher)) {
      report.error('ids.windows_publisher', r'must not contain \ " $ or ;');
    }
  }
  if (linuxId != null && !_linuxId.hasMatch(linuxId)) {
    report.error(
      'ids.linux',
      'must look like com.company.App: at least two parts using letters, digits, _ and - (found "$linuxId")',
    );
  }

  // --- look
  final look = top.section('look');
  final iconFile = look.string('icon');
  final symbolFile = look.string('symbol');
  final iconBackgroundRaw = look.string('icon_background');
  final primaryColorRaw = look.string('primary_color');
  look.finish();

  Rgb? colour(String? raw, String where) {
    if (raw == null) return null;
    if (!_color.hasMatch(raw)) {
      report.error(
        where,
        'must be a colour like "#F97316" (6 hex digits, quoted; found "$raw")',
      );
      return null;
    }
    return Rgb.parse(raw);
  }

  final iconBackground = colour(iconBackgroundRaw, 'look.icon_background');
  final primaryColor = colour(primaryColorRaw, 'look.primary_color');

  // --- defaults
  final defaults = top.section('defaults');
  final serverUrlRaw = defaults.string('server_url', required: false);
  final allowCustomServers = defaults.boolean('allow_custom_servers');
  final pushRelayRaw = defaults.string('push_relay_url', required: false);
  defaults.finish();

  final serverUrl = _url(
    report,
    'defaults.server_url',
    serverUrlRaw,
    allowLoopbackHttp: true,
    plainOrigin: true,
  );
  final pushRelayUrl = _url(
    report,
    'defaults.push_relay_url',
    pushRelayRaw,
    allowLoopbackHttp: true,
    plainOrigin: true,
  );
  if (serverUrl != null && serverUrl.isEmpty && allowCustomServers == false) {
    report.error(
      'defaults.allow_custom_servers',
      'is false, but defaults.server_url is empty: the app would have no server to connect to',
    );
  }

  // --- links
  final links = top.section('links');
  final website = _url(
    report,
    'links.website',
    links.string('website', required: false),
  );
  final support = _url(
    report,
    'links.support',
    links.string('support', required: false),
  );
  final privacyPolicy = _url(
    report,
    'links.privacy_policy',
    links.string('privacy_policy', required: false),
  );
  final sourceCode = _url(
    report,
    'links.source_code',
    links.string('source_code', required: false),
  );
  links.finish();

  // --- firebase (optional)
  final firebase = top.section('firebase', required: false);
  final firebaseAndroid = firebase.string('android_config', required: false);
  firebase.finish();
  top.finish();

  if (report.errors.length > errorsBefore) return null;

  return BrandSpec(
    name: name!,
    shortName: shortName!,
    description: description!,
    urlScheme: urlScheme!,
    appLinksDomain: appLinksDomain!,
    androidId: androidId!,
    appleId: appleId!,
    appleTeamId: appleTeamId!,
    windowsId: windowsId!,
    windowsPublisher: windowsPublisher!,
    linuxId: linuxId!,
    iconFile: iconFile!,
    symbolFile: symbolFile!,
    iconBackground: iconBackground!,
    primaryColor: primaryColor!,
    serverUrl: serverUrl!,
    allowCustomServers: allowCustomServers!,
    pushRelayUrl: pushRelayUrl!,
    website: website!,
    support: support!,
    privacyPolicy: privacyPolicy!,
    sourceCode: sourceCode!,
    firebaseAndroidConfig: firebaseAndroid!,
  );
}

/// Validates an optional URL; returns it normalised ('' if unset), or null after
/// reporting a problem.
///
/// URLs must be https. With [allowLoopbackHttp], plain http is accepted for
/// local development hosts. With [plainOrigin], credentials, query strings and
/// fragments are rejected and a trailing slash is dropped.
String? _url(
  Report report,
  String where,
  String? raw, {
  bool allowLoopbackHttp = false,
  bool plainOrigin = false,
}) {
  if (raw == null) return null;
  if (raw.isEmpty) return '';
  final uri = Uri.tryParse(raw);
  if (uri == null || !uri.hasAuthority || uri.host.isEmpty) {
    report.error(
      where,
      'must be a full URL like https://example.org (found "$raw")',
    );
    return null;
  }
  final httpOk =
      allowLoopbackHttp && uri.scheme == 'http' && _isLoopback(uri.host);
  if (uri.scheme != 'https' && !httpOk) {
    report.error(
      where,
      allowLoopbackHttp
          ? 'must use https (http is only accepted for localhost; found "$raw")'
          : 'must use https (found "$raw")',
    );
    return null;
  }
  if (uri.userInfo.isNotEmpty) {
    report.error(where, 'must not contain a user name or password');
    return null;
  }
  if (plainOrigin && (uri.hasQuery || uri.hasFragment)) {
    report.error(
      where,
      'must not contain a ? query or # fragment (found "$raw")',
    );
    return null;
  }
  return plainOrigin && raw.endsWith('/')
      ? raw.substring(0, raw.length - 1)
      : raw;
}
