import 'package:flutter/widgets.dart';

/// Material 3 window size classes, from the window's width in logical pixels.
/// The layout follows the window, not the device (see docs/APP-DESIGN.md).
enum WindowClass {
  /// Under 600: bottom bar, one pane.
  compact,

  /// 600 to 839: navigation rail, one pane at a time.
  medium,

  /// 840 to 1199: rail, topic list and thread side by side.
  expanded,

  /// 1200 and up: sidebar tree, thread and a details pane.
  large;

  static const double mediumStart = 600;
  static const double expandedStart = 840;
  static const double largeStart = 1200;

  static WindowClass fromWidth(double width) {
    if (width < mediumStart) return compact;
    if (width < expandedStart) return medium;
    if (width < largeStart) return expanded;
    return large;
  }

  /// Whether a list and the thread it opens are shown side by side.
  bool get hasTwoPanes => this == expanded || this == large;
}

extension WindowClassOf on BuildContext {
  WindowClass get windowClass =>
      WindowClass.fromWidth(MediaQuery.sizeOf(this).width);
}
