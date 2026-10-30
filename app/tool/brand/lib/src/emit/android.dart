import '../icons.dart';
import '../loader.dart';
import '../outputs.dart';

/// Android screen densities and their scale relative to mdpi.
const _densities = {
  'mdpi': 1.0,
  'hdpi': 1.5,
  'xhdpi': 2.0,
  'xxhdpi': 3.0,
  'xxxhdpi': 4.0,
};

int _px(num dp, double scale) => (dp * scale).round();

/// Escapes [value] for a Java `.properties` file, which Gradle reads as
/// ISO-8859-1: anything outside ASCII becomes a `\uXXXX` escape.
String propertiesValue(String value) {
  final b = StringBuffer();
  for (final unit in value.codeUnits) {
    switch (unit) {
      case 0x5C:
        b.write(r'\\');
      case 0x0A:
        b.write(r'\n');
      case 0x0D:
        b.write(r'\r');
      case 0x09:
        b.write(r'\t');
      default:
        if (unit < 0x20 || unit > 0x7E) {
          b.write('\\u${unit.toRadixString(16).padLeft(4, '0')}');
        } else {
          b.writeCharCode(unit);
        }
    }
  }
  return b.toString();
}

/// Android: build properties, launcher / adaptive / notification icons and, when
/// the brand has one, `google-services.json`.
///
/// The icons go to `android/app/src/brand/res`, a resource folder that Gradle
/// adds to the build; the Kotlin package never changes, so no source moves.
void emitAndroid(Brand brand, IconFactory icons, Outputs out) {
  final s = brand.spec;
  const res = 'android/app/src/brand/res';

  out.text('android/brand.properties', '''
# $generatedNotice
applicationId=${propertiesValue(s.androidId)}
appName=${propertiesValue(s.shortName)}
urlScheme=${propertiesValue(s.urlScheme)}
appLinksDomain=${propertiesValue(s.appLinksDomain)}
''');

  out.text('$res/values/brand_colors.xml', '''
<?xml version="1.0" encoding="utf-8"?>
<!-- $generatedNotice -->
<resources>
    <color name="ic_launcher_background">${s.iconBackground.hex}</color>
    <color name="notification_accent">${s.primaryColor.hex}</color>
</resources>
''');

  out.text('$res/mipmap-anydpi-v26/ic_launcher.xml', '''
<?xml version="1.0" encoding="utf-8"?>
<!-- $generatedNotice -->
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background" />
    <foreground android:drawable="@mipmap/ic_launcher_foreground" />
    <monochrome android:drawable="@mipmap/ic_launcher_monochrome" />
</adaptive-icon>
''');

  for (final MapEntry(key: density, value: scale) in _densities.entries) {
    // Launcher icon for Android 7 and older: 48 dp.
    out.bytes(
      '$res/mipmap-$density/ic_launcher.png',
      icons.rounded(_px(48, scale)),
    );
    // Adaptive icon layers: 108 dp.
    out.bytes(
      '$res/mipmap-$density/ic_launcher_foreground.png',
      icons.foreground(_px(108, scale)),
    );
    out.bytes(
      '$res/mipmap-$density/ic_launcher_monochrome.png',
      icons.monochrome(_px(108, scale)),
    );
    // Status-bar icon: 24 dp.
    out.bytes(
      '$res/drawable-$density/ic_notification.png',
      icons.notification(_px(24, scale)),
    );
  }

  final googleServices = brand.googleServices;
  if (googleServices != null) {
    out.bytes('android/app/google-services.json', googleServices);
  }
}
