import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../data/providers.dart';
import '../../l10n/app_localizations.dart';
import 'server_check_form.dart';
import 'sign_in_form.dart';

/// Closes a sheet or dialog; a link that opened it alone has nothing to pop.
void closeModal(BuildContext context) =>
    context.canPop() ? context.pop() : context.go('/');

Widget _modalBody(
  BuildContext context, {
  required String title,
  required Widget child,
}) {
  return SingleChildScrollView(
    padding: EdgeInsets.fromLTRB(
      24,
      8,
      24,
      24 + MediaQuery.viewInsetsOf(context).bottom,
    ),
    child: Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(title, style: Theme.of(context).textTheme.titleLarge),
        const SizedBox(height: 16),
        child,
      ],
    ),
  );
}

/// `/signin/:id`, as a sheet or a dialog.
class SignInModal extends ConsumerWidget {
  const SignInModal({required this.serverId, super.key});

  final int serverId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final server = ref.watch(serverProvider(serverId)).value;
    if (server == null) {
      return _modalBody(
        context,
        title: l10n.signInSubmit,
        child: const SizedBox.shrink(),
      );
    }
    return _modalBody(
      context,
      title: l10n.signInTitle(server.host),
      child: SignInForm(server: server, onSignedIn: () => closeModal(context)),
    );
  }
}

/// `/add-server`, as a sheet or a dialog.
class AddServerModal extends StatelessWidget {
  const AddServerModal({super.key});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    return _modalBody(
      context,
      title: l10n.serversAdd,
      child: ServerCheckForm(
        refuseKnown: true,
        onAdded: (server, info) {
          final router = GoRouter.of(context);
          closeModal(context);
          router.go('/settings/servers/${server.id}');
        },
      ),
    );
  }
}
