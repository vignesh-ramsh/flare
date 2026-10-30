import 'dart:math' as math;
import 'dart:typed_data';

/// An opaque colour.
final class Rgb {
  const Rgb(this.r, this.g, this.b);

  /// Parses `#RRGGBB`.
  factory Rgb.parse(String hex) {
    final m = RegExp(r'^#([0-9a-fA-F]{6})$').firstMatch(hex);
    if (m == null) throw FormatException('Expected #RRGGBB', hex);
    final v = int.parse(m[1]!, radix: 16);
    return Rgb((v >> 16) & 0xFF, (v >> 8) & 0xFF, v & 0xFF);
  }

  static const white = Rgb(255, 255, 255);
  static const black = Rgb(0, 0, 0);

  final int r;
  final int g;
  final int b;

  /// `0xAARRGGBB` with full opacity, the layout of Flutter's `Color`.
  int get argb => 0xFF000000 | (r << 16) | (g << 8) | b;

  /// `#RRGGBB`, upper case.
  String get hex =>
      '#${((r << 16) | (g << 8) | b).toRadixString(16).padLeft(6, '0').toUpperCase()}';

  @override
  bool operator ==(Object other) =>
      other is Rgb && other.r == r && other.g == g && other.b == b;

  @override
  int get hashCode => Object.hash(r, g, b);

  @override
  String toString() => hex;
}

/// An 8-bit RGBA image with straight (non-premultiplied) alpha.
///
/// Only what the icon pipeline needs: resampling, alpha compositing, masking
/// and recolouring. Resampling works on premultiplied values, so transparent
/// edges never pick up a dark or light fringe.
final class Raster {
  Raster(this.width, this.height, [Uint8List? pixels])
    : pixels = pixels ?? Uint8List(width * height * 4);

  /// A fully opaque image of one colour.
  factory Raster.solid(int width, int height, Rgb color) {
    final raster = Raster(width, height);
    final px = raster.pixels;
    for (var i = 0; i < px.length; i += 4) {
      px[i] = color.r;
      px[i + 1] = color.g;
      px[i + 2] = color.b;
      px[i + 3] = 255;
    }
    return raster;
  }

  final int width;
  final int height;

  /// `width * height * 4` bytes: R, G, B, A for each pixel, row by row.
  final Uint8List pixels;

  bool get isSquare => width == height;

  Raster copy() => Raster(width, height, Uint8List.fromList(pixels));

  /// The share of pixels (0 to 1) whose alpha satisfies [test].
  double alphaShare(bool Function(int alpha) test) {
    var count = 0;
    for (var i = 3; i < pixels.length; i += 4) {
      if (test(pixels[i])) count++;
    }
    return count / (width * height);
  }

  /// Draws [top] over this image, with its top-left corner at ([dx], [dy]).
  void drawOver(Raster top, {int dx = 0, int dy = 0}) {
    for (var y = 0; y < top.height; y++) {
      final ty = y + dy;
      if (ty < 0 || ty >= height) continue;
      for (var x = 0; x < top.width; x++) {
        final tx = x + dx;
        if (tx < 0 || tx >= width) continue;
        final s = (y * top.width + x) * 4;
        final d = (ty * width + tx) * 4;
        final sa = top.pixels[s + 3];
        if (sa == 0) continue;
        if (sa == 255) {
          pixels.setRange(d, d + 4, top.pixels, s);
          continue;
        }
        final sf = sa / 255;
        final df = pixels[d + 3] / 255;
        final of = sf + df * (1 - sf);
        for (var c = 0; c < 3; c++) {
          final v =
              (top.pixels[s + c] * sf + pixels[d + c] * df * (1 - sf)) / of;
          pixels[d + c] = v.round().clamp(0, 255);
        }
        pixels[d + 3] = (of * 255).round().clamp(0, 255);
      }
    }
  }

  /// A copy whose alpha is multiplied by [coverage] (one value from 0 to 1 per
  /// pixel).
  Raster masked(Float32List coverage) {
    final out = copy();
    for (var i = 0; i < width * height; i++) {
      out.pixels[i * 4 + 3] = (pixels[i * 4 + 3] * coverage[i]).round();
    }
    return out;
  }

  /// A copy with every pixel set to [color], keeping the alpha channel.
  Raster recolored(Rgb color) {
    final out = copy();
    for (var i = 0; i < out.pixels.length; i += 4) {
      out.pixels[i] = color.r;
      out.pixels[i + 1] = color.g;
      out.pixels[i + 2] = color.b;
    }
    return out;
  }

  /// A copy with the alpha channel forced to fully opaque.
  Raster opaque() {
    final out = copy();
    for (var i = 3; i < out.pixels.length; i += 4) {
      out.pixels[i] = 255;
    }
    return out;
  }

  /// Crops to the visible shape (pixels with alpha of at least [threshold]),
  /// as a square centred on it. Empty images are returned unchanged.
  Raster trimmedToSquare({int threshold = 8}) {
    var minX = width, minY = height, maxX = -1, maxY = -1;
    for (var y = 0; y < height; y++) {
      for (var x = 0; x < width; x++) {
        if (pixels[(y * width + x) * 4 + 3] >= threshold) {
          if (x < minX) minX = x;
          if (x > maxX) maxX = x;
          if (y < minY) minY = y;
          if (y > maxY) maxY = y;
        }
      }
    }
    if (maxX < 0) return this;
    final side = math.max(maxX - minX + 1, maxY - minY + 1);
    final left = (minX + maxX + 1 - side) ~/ 2;
    final top = (minY + maxY + 1 - side) ~/ 2;
    final out = Raster(side, side);
    for (var y = 0; y < side; y++) {
      final sy = y + top;
      if (sy < 0 || sy >= height) continue;
      for (var x = 0; x < side; x++) {
        final sx = x + left;
        if (sx < 0 || sx >= width) continue;
        out.pixels.setRange(
          (y * side + x) * 4,
          (y * side + x) * 4 + 4,
          pixels,
          (sy * width + sx) * 4,
        );
      }
    }
    return out;
  }

  /// Scales this square image to [fraction] of a transparent [canvas]-pixel
  /// square and centres it there.
  Raster centeredIn(int canvas, double fraction) {
    assert(isSquare);
    final inner = (canvas * fraction).round();
    final out = Raster(canvas, canvas);
    out.drawOver(
      resized(inner, inner),
      dx: (canvas - inner) ~/ 2,
      dy: (canvas - inner) ~/ 2,
    );
    return out;
  }

  /// Resamples to [newWidth] x [newHeight] with a triangle filter, widened when
  /// shrinking so every source pixel contributes.
  Raster resized(int newWidth, int newHeight) {
    if (newWidth == width && newHeight == height) return this;
    final premultiplied = Float32List(width * height * 4);
    for (var i = 0; i < width * height; i++) {
      final a = pixels[i * 4 + 3] / 255;
      premultiplied[i * 4] = pixels[i * 4] / 255 * a;
      premultiplied[i * 4 + 1] = pixels[i * 4 + 1] / 255 * a;
      premultiplied[i * 4 + 2] = pixels[i * 4 + 2] / 255 * a;
      premultiplied[i * 4 + 3] = a;
    }

    // Horizontal pass: width -> newWidth.
    final wide = Float32List(newWidth * height * 4);
    for (final (x, taps) in _taps(width, newWidth).indexed) {
      for (var y = 0; y < height; y++) {
        final out = (y * newWidth + x) * 4;
        for (var k = 0; k < taps.weights.length; k++) {
          final src = (y * width + taps.start + k) * 4;
          final w = taps.weights[k];
          wide[out] += premultiplied[src] * w;
          wide[out + 1] += premultiplied[src + 1] * w;
          wide[out + 2] += premultiplied[src + 2] * w;
          wide[out + 3] += premultiplied[src + 3] * w;
        }
      }
    }

    // Vertical pass: height -> newHeight.
    final result = Raster(newWidth, newHeight);
    for (final (y, taps) in _taps(height, newHeight).indexed) {
      for (var x = 0; x < newWidth; x++) {
        var r = 0.0, g = 0.0, b = 0.0, a = 0.0;
        for (var k = 0; k < taps.weights.length; k++) {
          final src = ((taps.start + k) * newWidth + x) * 4;
          final w = taps.weights[k];
          r += wide[src] * w;
          g += wide[src + 1] * w;
          b += wide[src + 2] * w;
          a += wide[src + 3] * w;
        }
        final out = (y * newWidth + x) * 4;
        if (a <= 0) continue; // stays transparent black
        result.pixels[out] = (r / a * 255).round().clamp(0, 255);
        result.pixels[out + 1] = (g / a * 255).round().clamp(0, 255);
        result.pixels[out + 2] = (b / a * 255).round().clamp(0, 255);
        result.pixels[out + 3] = (a * 255).round().clamp(0, 255);
      }
    }
    return result;
  }
}

/// Source pixels, and their weights, that make up one output pixel.
final class _Taps {
  _Taps(this.start, this.weights);
  final int start;
  final Float64List weights;
}

List<_Taps> _taps(int from, int to) {
  final scale = from / to;
  final support = math.max(scale, 1.0);
  return List.generate(to, (i) {
    final center = (i + 0.5) * scale;
    final start = math.max(0, (center - support).floor());
    final end = math.min(from - 1, (center + support).ceil());
    final weights = Float64List(end - start + 1);
    var sum = 0.0;
    for (var x = start; x <= end; x++) {
      final w = math.max(0.0, 1 - ((x + 0.5) - center).abs() / support);
      weights[x - start] = w;
      sum += w;
    }
    for (var k = 0; k < weights.length; k++) {
      weights[k] /= sum;
    }
    return _Taps(start, weights);
  });
}

/// Anti-aliased coverage (0 to 1 per pixel) of a rounded rectangle that fills a
/// [width] x [height] image, with corners of the given [radius] in pixels.
Float32List roundedRectCoverage(int width, int height, double radius) {
  final coverage = Float32List(width * height);
  final halfW = width / 2, halfH = height / 2;
  for (var y = 0; y < height; y++) {
    for (var x = 0; x < width; x++) {
      final px = (x + 0.5 - halfW).abs() - (halfW - radius);
      final py = (y + 0.5 - halfH).abs() - (halfH - radius);
      final outside = math.sqrt(
        math.max(px, 0) * math.max(px, 0) + math.max(py, 0) * math.max(py, 0),
      );
      final distance = outside + math.min(math.max(px, py), 0) - radius;
      coverage[y * width + x] = (0.5 - distance).clamp(0.0, 1.0);
    }
  }
  return coverage;
}
