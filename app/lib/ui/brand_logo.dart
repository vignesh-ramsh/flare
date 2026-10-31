import 'package:flutter/material.dart';

import '../brand/brand.g.dart';
import '../l10n/app_localizations.dart';

/// The brand's logo from `assets/brand/logo.png` (made by `make brand`).
class BrandLogo extends StatelessWidget {
  const BrandLogo({required this.size, super.key});

  final double size;

  @override
  Widget build(BuildContext context) {
    final label = AppLocalizations.of(context).logoLabel(Brand.name);
    return Semantics(
      label: label,
      image: true,
      excludeSemantics: true,
      child: ClipRRect(
        borderRadius: BorderRadius.circular(size / 4),
        child: Image.asset(
          'assets/brand/logo.png',
          width: size,
          height: size,
          fit: BoxFit.cover,
          // A build without generated assets still runs; it shows the first letter.
          errorBuilder: (context, error, stack) => Container(
            width: size,
            height: size,
            color: Color(Brand.iconBackgroundColor),
            alignment: Alignment.center,
            child: Text(
              Brand.shortName.characters.first,
              style: TextStyle(
                color: Colors.white,
                fontSize: size / 2,
                fontWeight: FontWeight.w700,
              ),
            ),
          ),
        ),
      ),
    );
  }
}
