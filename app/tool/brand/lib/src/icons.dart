import 'dart:typed_data';

import 'image_io.dart';
import 'loader.dart';
import 'raster.dart';

/// Share of the canvas that an Android adaptive icon guarantees to keep
/// visible: a 66 dp circle in the 108 dp layer.
const _adaptiveSafeZone = 66 / 108;

/// Produces every icon variant from the brand's two source images.
///
/// `icon.png` is artwork on a transparent (or opaque) 1024 px canvas, kept inside
/// the central two thirds. `symbol.png` is a silhouette. Each platform wants a
/// different treatment, so each has its own method:
///
/// - [ios]: square, opaque, no alpha channel; iOS rounds the corners itself.
/// - [macos]: rounded tile inset from the edge, as macOS expects.
/// - [rounded]: full-size rounded tile, for Android's legacy launcher icon,
///   Windows, Linux and the in-app logo.
/// - [foreground] and [monochrome]: the layers of an Android adaptive icon.
/// - [notification], [trayLight], [trayTemplate]: single-colour silhouettes.
final class IconFactory {
  IconFactory(Brand brand)
    : _icon = brand.icon,
      _symbol = brand.symbol,
      _background = brand.spec.iconBackground;

  final Raster _icon;
  final Raster _symbol;
  final Rgb _background;
  final Map<String, Uint8List> _png = {};

  late final Raster _flat = Raster.solid(masterSize, masterSize, _background)
    ..drawOver(_icon);
  late final Raster _roundedMaster = _tile(inset: 0, radiusShare: 0.225);
  late final Raster _macosMaster = _tile(inset: 100, radiusShare: 0.2237);
  late final Raster _whiteSymbol = _symbol.recolored(Rgb.white);
  late final Raster _blackSymbol = _symbol.recolored(Rgb.black);

  /// The background colour with the artwork on it, as a rounded tile whose
  /// edge is [inset] pixels from the canvas edge (at 1024 px).
  Raster _tile({required int inset, required double radiusShare}) {
    final inner = masterSize - 2 * inset;
    final tile = Raster.solid(inner, inner, _background)
      ..drawOver(_icon.resized(inner, inner));
    final shaped = tile.masked(
      roundedRectCoverage(inner, inner, inner * radiusShare),
    );
    return Raster(masterSize, masterSize)
      ..drawOver(shaped, dx: inset, dy: inset);
  }

  Uint8List _cached(String key, Raster Function() make, {bool alpha = true}) =>
      _png.putIfAbsent(key, () => encodePng(make(), alpha: alpha));

  Uint8List ios(int size) => _cached(
    'ios$size',
    () => _flat.resized(size, size).opaque(),
    alpha: false,
  );

  Uint8List macos(int size) =>
      _cached('macos$size', () => _macosMaster.resized(size, size));

  Uint8List rounded(int size) =>
      _cached('rounded$size', () => rasterRounded(size));

  /// The rounded tile as an image, for building `.ico` files.
  Raster rasterRounded(int size) => _roundedMaster.resized(size, size);

  /// Adaptive-icon foreground: the artwork as is, at [size] (the 108 dp layer).
  Uint8List foreground(int size) =>
      _cached('foreground$size', () => _icon.resized(size, size));

  /// Adaptive-icon monochrome layer, used for themed icons on Android 13+.
  Uint8List monochrome(int size) => _cached(
    'monochrome$size',
    () => _whiteSymbol.centeredIn(size, _adaptiveSafeZone),
  );

  /// Android status-bar icon: a white silhouette with a little padding.
  Uint8List notification(int size) =>
      _cached('notification$size', () => _whiteSymbol.centeredIn(size, 0.84));

  /// White silhouette for Linux tray icons.
  Uint8List trayLight(int size) =>
      _cached('trayLight$size', () => _whiteSymbol.centeredIn(size, 0.9));

  /// Black silhouette for the macOS menu bar, where the system tints "template"
  /// images to match the bar.
  Uint8List trayTemplate(int size) =>
      _cached('trayTemplate$size', () => _blackSymbol.centeredIn(size, 0.82));
}
