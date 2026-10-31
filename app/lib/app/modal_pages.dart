import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import 'window_class.dart';

/// A route shown as a bottom sheet, for [GoRoute.pageBuilder].
class SheetPage<T> extends Page<T> {
  const SheetPage({required this.child, super.key, super.name});

  final Widget child;

  @override
  Route<T> createRoute(BuildContext context) => ModalBottomSheetRoute<T>(
    settings: this,
    isScrollControlled: true,
    showDragHandle: true,
    useSafeArea: true,
    builder: (_) => child,
  );
}

/// A route shown as a dialog, for [GoRoute.pageBuilder].
class DialogPage<T> extends Page<T> {
  const DialogPage({required this.child, super.key, super.name});

  final Widget child;

  @override
  Route<T> createRoute(BuildContext context) => DialogRoute<T>(
    context: context,
    settings: this,
    builder: (_) => Dialog(
      clipBehavior: Clip.antiAlias,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 480),
        child: child,
      ),
    ),
  );
}

/// A sheet in compact and medium windows, a dialog in expanded and large ones.
Page<void> adaptiveModalPage(
  BuildContext context,
  GoRouterState state,
  Widget child,
) {
  return context.windowClass.hasTwoPanes
      ? DialogPage<void>(key: state.pageKey, child: child)
      : SheetPage<void>(key: state.pageKey, child: child);
}
