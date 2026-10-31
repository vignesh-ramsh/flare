import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter/services.dart';

import '../../auth/auth_service.dart';
import '../../core/db/database.dart';
import '../../l10n/app_localizations.dart';
import '../../net/api_client.dart';
import 'api_error_text.dart';

/// User name and password for a server. The password is used once and not kept; what the
/// app saves is the token the server returns. A server on plain http gets a warning, and
/// the button says "Sign in anyway".
class SignInForm extends ConsumerStatefulWidget {
  const SignInForm({
    required this.server,
    required this.onSignedIn,
    this.onSkip,
    super.key,
  });

  final ServerRow server;
  final VoidCallback onSignedIn;

  /// Adds a "Skip for now" button, for the first run where signing in is optional.
  final VoidCallback? onSkip;

  @override
  ConsumerState<SignInForm> createState() => _SignInFormState();
}

class _SignInFormState extends ConsumerState<SignInForm> {
  final _formKey = GlobalKey<FormState>();
  final _user = TextEditingController();
  final _password = TextEditingController();
  bool _hidden = true;
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _user.dispose();
    _password.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() => _error = null);
    if (!_formKey.currentState!.validate()) return;

    setState(() => _busy = true);
    final l10n = AppLocalizations.of(context);
    try {
      await ref
          .read(authServiceProvider)
          .signIn(
            widget.server,
            user: _user.text.trim(),
            password: _password.text,
          );
      TextInput.finishAutofillContext();
      if (mounted) widget.onSignedIn();
    } on ApiException catch (error) {
      if (mounted) {
        setState(() {
          _busy = false;
          _error = apiErrorText(l10n, error, widget.server.host);
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final plainHttp = Uri.parse(widget.server.url).scheme == 'http';

    return AutofillGroup(
      child: Form(
        key: _formKey,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (plainHttp) ...[
              Semantics(
                liveRegion: true,
                container: true,
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    color: scheme.errorContainer,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Padding(
                    padding: const EdgeInsets.all(12),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Icon(
                          Icons.warning_amber,
                          size: 20,
                          color: scheme.onErrorContainer,
                        ),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Text(
                            l10n.signInHttpWarning(widget.server.host),
                            style: theme.textTheme.bodyMedium?.copyWith(
                              color: scheme.onErrorContainer,
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 16),
            ],
            TextFormField(
              controller: _user,
              autofocus: true,
              autocorrect: false,
              enableSuggestions: false,
              autofillHints: const [AutofillHints.username],
              textInputAction: TextInputAction.next,
              decoration: InputDecoration(labelText: l10n.signInUser),
              validator: (value) => (value == null || value.trim().isEmpty)
                  ? l10n.signInUserRequired
                  : null,
            ),
            const SizedBox(height: 16),
            TextFormField(
              controller: _password,
              obscureText: _hidden,
              autocorrect: false,
              enableSuggestions: false,
              autofillHints: const [AutofillHints.password],
              textInputAction: TextInputAction.done,
              onFieldSubmitted: (_) => _submit(),
              decoration: InputDecoration(
                labelText: l10n.signInPassword,
                suffixIcon: IconButton(
                  tooltip: _hidden ? l10n.showPassword : l10n.hidePassword,
                  icon: Icon(
                    _hidden
                        ? Icons.visibility_outlined
                        : Icons.visibility_off_outlined,
                  ),
                  onPressed: () => setState(() => _hidden = !_hidden),
                ),
              ),
              validator: (value) => (value == null || value.isEmpty)
                  ? l10n.signInPasswordRequired
                  : null,
            ),
            if (_error != null) ...[
              const SizedBox(height: 12),
              Semantics(
                liveRegion: true,
                child: Text(
                  _error!,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: scheme.error,
                  ),
                ),
              ),
            ],
            const SizedBox(height: 24),
            // A Wrap, so longer labels (other languages, large text) move to a second line
            // instead of overflowing.
            Align(
              alignment: Alignment.centerRight,
              child: Wrap(
                alignment: WrapAlignment.end,
                spacing: 8,
                runSpacing: 8,
                children: [
                  if (widget.onSkip != null)
                    TextButton(
                      onPressed: _busy ? null : widget.onSkip,
                      child: Text(l10n.welcomeSkip),
                    ),
                  FilledButton(
                    onPressed: _busy ? null : _submit,
                    child: Text(
                      plainHttp ? l10n.signInSubmitHttp : l10n.signInSubmit,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
