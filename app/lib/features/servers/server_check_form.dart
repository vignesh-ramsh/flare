import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/db/database.dart';
import '../../data/providers.dart';
import '../../data/server_url.dart';
import '../../l10n/app_localizations.dart';
import '../../net/api_client.dart';
import '../../net/providers.dart';
import 'api_error_text.dart';

/// Asks for a server's address, checks that it answers like a server this app can use,
/// shows what it found, and adds it on "Continue". A build locked to one server shows no
/// address field and checks that server.
class ServerCheckForm extends ConsumerStatefulWidget {
  const ServerCheckForm({
    required this.onAdded,
    this.refuseKnown = false,
    super.key,
  });

  /// Called with the server once it is added (or was already there).
  final void Function(ServerRow server, ServerInfo info) onAdded;

  /// Say so, instead of carrying on, when the server was already added.
  final bool refuseKnown;

  @override
  ConsumerState<ServerCheckForm> createState() => _ServerCheckFormState();
}

class _ServerCheckFormState extends ConsumerState<ServerCheckForm> {
  final _formKey = GlobalKey<FormState>();
  final _address = TextEditingController();
  bool _busy = false;
  String? _error;
  ServerInfo? _found;
  ServerAddress? _checked;

  @override
  void initState() {
    super.initState();
    _address.text = ref.read(serverPolicyProvider).defaultUrl;
  }

  @override
  void dispose() {
    _address.dispose();
    super.dispose();
  }

  ServerAddress? get _typed => ServerAddress.parse(_address.text);

  Future<void> _check() async {
    setState(() {
      _error = null;
      _found = null;
    });
    final policy = ref.read(serverPolicyProvider);
    if (policy.allowCustom && !_formKey.currentState!.validate()) return;

    final address = policy.allowCustom
        ? _typed
        : ServerAddress.parse(policy.defaultUrl);
    if (address == null) return;

    final l10n = AppLocalizations.of(context);
    setState(() => _busy = true);
    try {
      final info = await ref.read(apiProvider).config(Uri.parse(address.url));
      if (!mounted) return;
      setState(() {
        _busy = false;
        _found = info;
        _checked = address;
      });
    } on ApiException catch (error) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = apiErrorText(l10n, error, address.host);
      });
    }
  }

  Future<void> _continue() async {
    final address = _checked;
    final info = _found;
    if (address == null || info == null) return;
    final l10n = AppLocalizations.of(context);

    final added = await ref.read(serversRepositoryProvider).add(address);
    if (!mounted) return;
    if (!added.created && widget.refuseKnown) {
      setState(() => _error = l10n.serverAlreadyAdded(address.host));
      return;
    }
    widget.onAdded(added.server, info);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final policy = ref.watch(serverPolicyProvider);
    final found = _found;

    return Form(
      key: _formKey,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (policy.allowCustom) ...[
            TextFormField(
              controller: _address,
              autofocus: true,
              keyboardType: TextInputType.url,
              autocorrect: false,
              enableSuggestions: false,
              textInputAction: TextInputAction.go,
              onFieldSubmitted: (_) =>
                  _busy ? null : (found == null ? _check() : _continue()),
              onChanged: (_) {
                if (_found != null || _error != null) {
                  setState(() {
                    _found = null;
                    _error = null;
                  });
                }
              },
              decoration: InputDecoration(
                labelText: l10n.addServerLabel,
                hintText: l10n.addServerHint,
                helperText: l10n.addServerHelper,
                helperMaxLines: 2,
              ),
              validator: (value) {
                if (value == null || value.trim().isEmpty) {
                  return l10n.addErrorServerRequired;
                }
                if (ServerAddress.parse(value) == null) {
                  return l10n.addErrorServerInvalid;
                }
                return null;
              },
            ),
          ] else
            Text(policy.defaultUrl, style: theme.textTheme.bodyLarge),
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
          if (found != null) ...[
            const SizedBox(height: 16),
            Semantics(
              liveRegion: true,
              container: true,
              child: Card(
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Icon(Icons.check_circle_outline, color: scheme.primary),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              l10n.serverFound(found.name),
                              style: theme.textTheme.titleMedium,
                            ),
                            const SizedBox(height: 4),
                            Text(
                              found.needsSignIn
                                  ? l10n.serverSignInRequired
                                  : l10n.serverSignInOptional,
                              style: theme.textTheme.bodyMedium?.copyWith(
                                color: scheme.onSurfaceVariant,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ],
          const SizedBox(height: 24),
          Align(
            alignment: Alignment.centerRight,
            child: found == null
                ? FilledButton(
                    onPressed: _busy ? null : _check,
                    child: Text(_busy ? l10n.serverChecking : l10n.serverCheck),
                  )
                : FilledButton(
                    onPressed: _continue,
                    child: Text(l10n.continueAction),
                  ),
          ),
        ],
      ),
    );
  }
}
