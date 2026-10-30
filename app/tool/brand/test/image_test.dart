import 'dart:io';
import 'dart:typed_data';

import 'package:brand_tool/src/image_io.dart';
import 'package:brand_tool/src/raster.dart';
import 'package:path/path.dart' as p;
import 'package:test/test.dart';

(int, int, int, int) pixel(Raster r, int x, int y) {
  final i = (y * r.width + x) * 4;
  return (r.pixels[i], r.pixels[i + 1], r.pixels[i + 2], r.pixels[i + 3]);
}

Raster fixture(String name) {
  final file = File(p.join('test', 'fixtures', name));
  final image = decodePng(file.readAsBytesSync());
  expect(image, isNotNull, reason: '$name should decode');
  return image!;
}

void main() {
  group('decoding PNGs of every flavour', () {
    test('RGBA keeps transparency and partial alpha', () {
      final image = fixture('rgba.png');
      expect(pixel(image, 0, 0), (255, 0, 0, 255));
      expect(pixel(image, 1, 0), (0, 255, 0, 128));
      expect(pixel(image, 7, 7).$4, 0);
    });

    test('RGB without alpha becomes fully opaque', () {
      final image = fixture('rgb.png');
      expect(pixel(image, 0, 0), (255, 0, 0, 255));
      expect(pixel(image, 7, 7), (1, 2, 3, 255));
    });

    test('grayscale is expanded, with or without alpha', () {
      expect(pixel(fixture('gray.png'), 3, 3), (77, 77, 77, 255));
      final withAlpha = fixture('gray_alpha.png');
      expect(pixel(withAlpha, 0, 0).$4, 255);
      expect(pixel(withAlpha, 7, 7).$4, 0);
    });

    test('palette images resolve colours and their transparency', () {
      final image = fixture('palette.png');
      expect(pixel(image, 0, 0), (0, 0, 255, 255));
      expect(pixel(image, 7, 7).$4, 0);
      expect(pixel(fixture('palette_opaque.png'), 7, 7), (0, 0, 255, 255));
    });

    test('16-bit images are reduced to 8 bits', () {
      final image = fixture('rgba16.png');
      expect(pixel(image, 0, 0), (255, 0, 0, 255));
      expect(pixel(image, 7, 7).$4, 0);
    });

    test('garbage is rejected rather than thrown', () {
      expect(looksLikePng(Uint8List.fromList([1, 2, 3])), isFalse);
      final damaged = File(
        p.join('test', 'fixtures', 'rgba.png'),
      ).readAsBytesSync().sublist(0, 60);
      expect(decodePng(damaged), isNull);
      expect(
        decodePng(
          Uint8List.fromList([
            0x89,
            0x50,
            0x4E,
            0x47,
            0x0D,
            0x0A,
            0x1A,
            0x0A,
            1,
            2,
            3,
          ]),
        ),
        isNull,
      );
    });
  });

  group('encoding PNGs', () {
    Raster gradient() {
      final image = Raster(16, 16);
      for (var y = 0; y < 16; y++) {
        for (var x = 0; x < 16; x++) {
          final i = (y * 16 + x) * 4;
          image.pixels[i] = x * 16;
          image.pixels[i + 1] = y * 16;
          image.pixels[i + 2] = 255 - x * 8;
          image.pixels[i + 3] = 255 - y * 12;
        }
      }
      return image;
    }

    test('RGBA survives a round trip exactly', () {
      final original = gradient();
      final decoded = decodePng(encodePng(original))!;
      expect(decoded.width, 16);
      expect(decoded.pixels, original.pixels);
    });

    test('colour type is RGBA, or RGB when alpha is dropped', () {
      // Byte 25 of a PNG is the colour type from the IHDR chunk: 6 = RGBA, 2 = RGB.
      expect(encodePng(gradient())[25], 6);
      expect(encodePng(gradient(), alpha: false)[25], 2);
    });

    test('RGB output has no alpha channel but keeps the colours', () {
      final opaque = gradient().opaque();
      final decoded = decodePng(encodePng(opaque, alpha: false))!;
      expect(decoded.pixels, opaque.pixels);
    });

    test('the same pixels always give the same bytes', () {
      expect(encodePng(gradient()), encodePng(gradient()));
    });
  });

  group('encoding ICO files', () {
    test('header and entries are little-endian and point at PNG data', () {
      final sizes = [16, 32, 256];
      final bytes = encodeIco([
        for (final s in sizes) Raster.solid(s, s, Rgb.white),
      ]);
      final data = ByteData.sublistView(bytes);
      const le = Endian.little;

      expect(data.getUint16(0, le), 0, reason: 'reserved');
      expect(data.getUint16(2, le), 1, reason: 'type: icon');
      expect(data.getUint16(4, le), 3, reason: 'image count');

      for (var i = 0; i < sizes.length; i++) {
        final entry = 6 + 16 * i;
        expect(
          data.getUint8(entry),
          sizes[i] == 256 ? 0 : sizes[i],
          reason: 'width (0 means 256)',
        );
        expect(data.getUint16(entry + 4, le), 1, reason: 'planes');
        expect(data.getUint16(entry + 6, le), 32, reason: 'bits per pixel');
        final length = data.getUint32(entry + 8, le);
        final offset = data.getUint32(entry + 12, le);
        final png = bytes.sublist(offset, offset + length);
        expect(looksLikePng(png), isTrue);
        expect(decodePng(png)!.width, sizes[i]);
      }
      final last = 6 + 16 * 2;
      expect(
        data.getUint32(last + 12, le) + data.getUint32(last + 8, le),
        bytes.length,
      );
    });
  });

  group('Raster', () {
    test(
      'resizing does not darken or lighten the edges of transparent shapes',
      () {
        // Left half opaque red, right half fully transparent with black RGB.
        final image = Raster(4, 4);
        for (var y = 0; y < 4; y++) {
          for (var x = 0; x < 2; x++) {
            image.pixels.setRange((y * 4 + x) * 4, (y * 4 + x) * 4 + 4, [
              255,
              0,
              0,
              255,
            ]);
          }
        }
        final (r, g, b, a) = pixel(image.resized(1, 1), 0, 0);
        expect(a, inInclusiveRange(126, 129));
        expect(
          r,
          greaterThan(250),
          reason: 'red must not be averaged with the transparent black',
        );
        expect(g, 0);
        expect(b, 0);
      },
    );

    test('resizing keeps flat opaque colour exactly, up and down', () {
      final flat = Raster.solid(10, 10, const Rgb(12, 34, 56));
      for (final size in [3, 7, 10, 25]) {
        final out = flat.resized(size, size);
        expect(pixel(out, 0, 0), (12, 34, 56, 255));
        expect(pixel(out, size - 1, size - 1), (12, 34, 56, 255));
      }
    });

    test('trimmedToSquare crops to the shape and centres it', () {
      final canvas = Raster(100, 100)
        ..drawOver(Raster.solid(10, 20, Rgb.black), dx: 30, dy: 40);
      final trimmed = canvas.trimmedToSquare();
      expect((trimmed.width, trimmed.height), (20, 20));
      // The 10-wide shape sits in the middle of the 20-wide square.
      expect(pixel(trimmed, 4, 10).$4, 0);
      expect(pixel(trimmed, 5, 10).$4, 255);
      expect(pixel(trimmed, 14, 10).$4, 255);
      expect(pixel(trimmed, 15, 10).$4, 0);
    });

    test('centeredIn scales and centres on a transparent canvas', () {
      final out = Raster.solid(10, 10, Rgb.white).centeredIn(100, 0.5);
      expect(pixel(out, 0, 0).$4, 0);
      expect(pixel(out, 50, 50), (255, 255, 255, 255));
      expect(pixel(out, 24, 50).$4, 0);
      expect(pixel(out, 25, 50).$4, 255);
    });

    test('drawOver blends partial alpha', () {
      final base = Raster.solid(1, 1, Rgb.black);
      base.drawOver(Raster(1, 1, Uint8List.fromList([255, 255, 255, 128])));
      expect(pixel(base, 0, 0), (128, 128, 128, 255));
    });

    test(
      'rounded-rectangle coverage rounds the corners and keeps the rest',
      () {
        final c = roundedRectCoverage(100, 100, 20);
        expect(c[0], 0.0, reason: 'extreme corner');
        expect(c[50 * 100 + 50], 1.0, reason: 'centre');
        expect(c[50], 1.0, reason: 'middle of the top edge');
        expect(
          c[2 * 100 + 2],
          lessThan(0.5),
          reason: 'inside the rounded corner',
        );
      },
    );
  });

  test('Rgb parses and formats', () {
    expect(Rgb.parse('#0F172A').argb, 0xFF0F172A);
    expect(const Rgb(15, 23, 42).hex, '#0F172A');
    expect(() => Rgb.parse('0F172A'), throwsFormatException);
  });
}
