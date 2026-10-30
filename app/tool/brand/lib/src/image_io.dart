import 'dart:io';
import 'dart:typed_data';

import 'package:image/image.dart' as img;

import 'raster.dart';

const _pngSignature = [0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A];

/// Whether [bytes] start with the PNG signature.
bool looksLikePng(Uint8List bytes) {
  if (bytes.length < _pngSignature.length) return false;
  for (var i = 0; i < _pngSignature.length; i++) {
    if (bytes[i] != _pngSignature[i]) return false;
  }
  return true;
}

/// Decodes a PNG of any bit depth or colour type, or returns null if it is
/// damaged.
Raster? decodePng(Uint8List bytes) {
  final img.Image? decoded;
  try {
    decoded = img.decodePng(bytes);
  } on Object {
    // Damaged files can surface as errors (such as a RangeError) as well as
    // exceptions; the caller reports "damaged" either way.
    return null;
  }
  if (decoded == null) return null;
  // No `alpha:` argument: it would overwrite the alpha of images that have one.
  final rgba = decoded.convert(format: img.Format.uint8, numChannels: 4);
  final raster = Raster(rgba.width, rgba.height);
  var i = 0;
  for (final pixel in rgba) {
    raster.pixels[i++] = pixel.r.toInt();
    raster.pixels[i++] = pixel.g.toInt();
    raster.pixels[i++] = pixel.b.toInt();
    raster.pixels[i++] = pixel.a.toInt();
  }
  return raster;
}

final List<int> _crcTable = List.generate(256, (n) {
  var c = n;
  for (var k = 0; k < 8; k++) {
    c = (c & 1) != 0 ? 0xEDB88320 ^ (c >> 1) : c >> 1;
  }
  return c;
});

int _crc32(List<int> bytes) {
  var c = 0xFFFFFFFF;
  for (final b in bytes) {
    c = _crcTable[(c ^ b) & 0xFF] ^ (c >> 8);
  }
  return c ^ 0xFFFFFFFF;
}

void _writeChunk(BytesBuilder out, String type, List<int> data) {
  final header = ByteData(4)..setUint32(0, data.length);
  final body = [...type.codeUnits, ...data];
  final crc = ByteData(4)..setUint32(0, _crc32(body));
  out
    ..add(header.buffer.asUint8List())
    ..add(body)
    ..add(crc.buffer.asUint8List());
}

/// Encodes [raster] as an 8-bit PNG: RGBA, or RGB without an alpha channel when
/// [alpha] is false (needed for iOS app icons, which the App Store rejects if
/// they have one).
///
/// The same pixels always produce the same bytes, so builds stay reproducible.
Uint8List encodePng(Raster raster, {bool alpha = true}) {
  final channels = alpha ? 4 : 3;
  final stride = raster.width * channels;
  final scanlines = Uint8List((stride + 1) * raster.height);
  var o = 0;
  for (var y = 0; y < raster.height; y++) {
    scanlines[o++] = 0; // filter type: none
    for (var x = 0; x < raster.width; x++) {
      final p = (y * raster.width + x) * 4;
      scanlines[o++] = raster.pixels[p];
      scanlines[o++] = raster.pixels[p + 1];
      scanlines[o++] = raster.pixels[p + 2];
      if (alpha) scanlines[o++] = raster.pixels[p + 3];
    }
  }

  final ihdr = ByteData(13)
    ..setUint32(0, raster.width)
    ..setUint32(4, raster.height)
    ..setUint8(8, 8) // bit depth
    ..setUint8(9, alpha ? 6 : 2) // colour type: RGBA / RGB
    ..setUint8(10, 0)
    ..setUint8(11, 0)
    ..setUint8(12, 0);

  final out = BytesBuilder()..add(_pngSignature);
  _writeChunk(out, 'IHDR', ihdr.buffer.asUint8List());
  _writeChunk(out, 'IDAT', ZLibCodec(level: 9).encode(scanlines));
  _writeChunk(out, 'IEND', const []);
  return out.toBytes();
}

/// Encodes a Windows `.ico` holding [images] (each square, at most 256 pixels),
/// stored as PNG.
Uint8List encodeIco(List<Raster> images) {
  // ICO is little-endian, unlike PNG.
  const le = Endian.little;
  final pngs = [for (final image in images) encodePng(image)];
  final header = ByteData(6 + 16 * images.length)
    ..setUint16(2, 1, le) // type: icon
    ..setUint16(4, images.length, le);
  var offset = header.lengthInBytes;
  for (var i = 0; i < images.length; i++) {
    final image = images[i];
    assert(image.isSquare && image.width <= 256);
    final entry = 6 + 16 * i;
    header
      ..setUint8(entry, image.width == 256 ? 0 : image.width)
      ..setUint8(entry + 1, image.height == 256 ? 0 : image.height)
      ..setUint16(entry + 4, 1, le) // colour planes
      ..setUint16(entry + 6, 32, le) // bits per pixel
      ..setUint32(entry + 8, pngs[i].length, le)
      ..setUint32(entry + 12, offset, le);
    offset += pngs[i].length;
  }
  final out = BytesBuilder()..add(header.buffer.asUint8List());
  pngs.forEach(out.add);
  return out.toBytes();
}
