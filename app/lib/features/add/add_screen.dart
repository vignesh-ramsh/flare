import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/shell.dart';
import '../../core/db/database.dart';
import '../../data/models.dart';
import '../../data/providers.dart';
import '../../data/server_url.dart';
import '../../l10n/app_localizations.dart';

/// Subscribe to a topic. Shown as a bottom sheet or a dialog by [adaptiveModalPage].
class AddScreen extends ConsumerStatefulWidget {
  const AddScreen({super.key});

  @override
  ConsumerState<AddScreen> createState() => _AddScreenState();
}

class _AddScreenState extends ConsumerState<AddScreen> {
  final _formKey = GlobalKey<FormState>();
  final _server = TextEditingController();
  final _topic = TextEditingController();
  bool _exists = false;
  bool _busy = false;
  bool _serverEdited = false;

  @override
  void initState() {
    super.initState();
    _server.text = ref.read(serverPolicyProvider).defaultUrl;
    // With no brand default, offer the server the user already has. A link straight
    // to this screen opens it before the database has answered, so wait for it.
    ref.listenManual(
      serversProvider,
      (previous, next) => _offerKnownServer(next.value),
      fireImmediately: true,
    );
  }

  void _offerKnownServer(List<ServerRow>? known) {
    if (_serverEdited || _server.text.isNotEmpty) return;
    if (known == null || known.isEmpty) return;
    _server.text = known.first.url;
  }

  /// Closes the sheet or dialog; a link that opened this screen alone has nothing to pop.
  void _close(GoRouter router) =>
      router.canPop() ? router.pop() : router.go('/');

  @override
  void dispose() {
    _server.dispose();
    _topic.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() => _exists = false);
    if (!_formKey.currentState!.validate()) return;

    final address = ServerAddress.parse(_server.text);
    if (address == null) return;
    final name = _topic.text.trim();

    setState(() => _busy = true);
    final router = GoRouter.of(context);
    final result = await ref
        .read(topicsRepositoryProvider)
        .subscribe(address, name);
    if (!mounted) return;
    setState(() => _busy = false);

    switch (result) {
      case SubscribeResult.alreadySubscribed:
        setState(() => _exists = true);
        _formKey.currentState!.validate();
      case SubscribeResult.added:
        _close(router);
        router.go(topicLocation(address.host, name));
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final policy = ref.watch(serverPolicyProvider);

    return SingleChildScrollView(
      padding: EdgeInsets.fromLTRB(
        24,
        8,
        24,
        24 + MediaQuery.viewInsetsOf(context).bottom,
      ),
      child: Form(
        key: _formKey,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(l10n.addTitle, style: theme.textTheme.titleLarge),
            const SizedBox(height: 16),
            if (policy.allowCustom) ...[
              TextFormField(
                controller: _server,
                keyboardType: TextInputType.url,
                autocorrect: false,
                textInputAction: TextInputAction.next,
                onChanged: (_) => _serverEdited = true,
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
              const SizedBox(height: 16),
            ],
            TextFormField(
              controller: _topic,
              autofocus: !policy.allowCustom || _server.text.isNotEmpty,
              autocorrect: false,
              textInputAction: TextInputAction.done,
              onFieldSubmitted: (_) => _submit(),
              decoration: InputDecoration(
                labelText: l10n.addTopicLabel,
                helperText: l10n.addTopicHelper,
              ),
              validator: (value) {
                final name = value?.trim() ?? '';
                if (name.isEmpty) return l10n.addErrorTopicRequired;
                if (!isValidTopic(name)) return l10n.addErrorTopicInvalid;
                if (_exists) return l10n.addErrorExists;
                return null;
              },
            ),
            const SizedBox(height: 24),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                TextButton(
                  onPressed: () => _close(GoRouter.of(context)),
                  child: Text(l10n.cancel),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: _busy ? null : _submit,
                  child: Text(l10n.subscribe),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
