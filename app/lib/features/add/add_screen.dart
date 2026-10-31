import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/routes.dart';
import '../../brand/brand.g.dart';
import '../../core/db/database.dart';
import '../../data/models.dart';
import '../../data/providers.dart';
import '../../data/server_url.dart';
import '../../data/subscribe_link.dart';
import '../../l10n/app_localizations.dart';
import '../../net/api_client.dart';
import '../../net/providers.dart';
import '../servers/api_error_text.dart';

/// Subscribe to a topic. Shown as a bottom sheet or a dialog by [adaptiveModalPage].
///
/// [server] and [topic] fill the form in, for a subscribe link or a QR code. The user still
/// decides: nothing is subscribed to until they press the button.
class AddScreen extends ConsumerStatefulWidget {
  const AddScreen({this.server, this.topic, super.key});

  /// A server address to start with, such as `https://example.com`.
  final String? server;
  final String? topic;

  @override
  ConsumerState<AddScreen> createState() => _AddScreenState();
}

class _AddScreenState extends ConsumerState<AddScreen> {
  final _formKey = GlobalKey<FormState>();
  final _topicKey = GlobalKey<FormFieldState<String>>();
  final _server = TextEditingController();
  final _topic = TextEditingController();
  bool _exists = false;
  bool _busy = false;
  bool _serverEdited = false;

  /// What the server said about itself (or why it did not), for the address in the form.
  _ServerNote? _note;
  Timer? _checkTimer;
  int _checkGeneration = 0;

  /// The host a build locked to one server insists on, when the form was asked for another.
  String? _lockedMismatch;

  @override
  void initState() {
    super.initState();
    final policy = ref.read(serverPolicyProvider);
    _server.text = policy.defaultUrl;
    _prefill(policy);
    _server.addListener(_serverChanged);
    // Whatever the form starts with is looked up too: a link may name a server the user has not
    // seen yet, and the note is what helps them decide.
    _serverChanged();
    // With no brand default, offer the server the user already has. A link straight
    // to this screen opens it before the database has answered, so wait for it.
    ref.listenManual(
      serversProvider,
      (previous, next) => _offerKnownServer(next.value),
      fireImmediately: true,
    );
  }

  /// Starts the form from a link's server and topic. A build locked to one server only takes a
  /// topic on that server; for any other it says so and leaves the form empty.
  void _prefill(ServerPolicy policy) {
    final server = widget.server;
    final topic = widget.topic;
    if (server != null) {
      final wanted = ServerAddress.parse(server);
      if (!policy.allowCustom) {
        final own = ServerAddress.parse(policy.defaultUrl);
        if (wanted != null && own != null && wanted.host != own.host) {
          _lockedMismatch = own.host;
          return;
        }
      } else if (wanted != null) {
        _server.text = wanted.url;
        _serverEdited = true;
      }
    }
    if (topic != null) _topic.text = topic;
  }

  /// Looks the server up a moment after the user stops typing its address.
  void _serverChanged() {
    _checkTimer?.cancel();
    _checkGeneration++;
    if (_note != null) setState(() => _note = null);
    if (ServerAddress.parse(_server.text) == null) return;
    _checkTimer = Timer(const Duration(milliseconds: 600), _check);
  }

  Future<void> _check() async {
    final address = ServerAddress.parse(_server.text);
    if (address == null) return;
    final generation = ++_checkGeneration;
    _ServerNote result;
    try {
      final info = await ref.read(apiProvider).config(Uri.parse(address.url));
      result = _ServerNote(host: address.host, info: info);
    } on ApiException catch (error) {
      result = _ServerNote(host: address.host, error: error);
    }
    // The user kept typing, or left: this answer is for an address no longer in the form.
    if (!mounted || generation != _checkGeneration) return;
    setState(() => _note = result);
  }

  /// A pasted subscribe link or topic address fills in both fields.
  void _fromPaste(String text) {
    final link = SubscribeLink.parse(text);
    if (link == null) return;
    final policy = ref.read(serverPolicyProvider);
    if (!policy.allowCustom) {
      final own = ServerAddress.parse(policy.defaultUrl);
      if (own == null || own.host != link.server.host) return;
    }
    _serverEdited = true;
    if (policy.allowCustom) _server.text = link.server.url;
    _topic.value = TextEditingValue(
      text: link.topic,
      selection: TextSelection.collapsed(offset: link.topic.length),
    );
    _topicEdited();
  }

  /// The topic changed other than by typing: any complaint about the old one no longer applies.
  void _topicEdited() {
    setState(() => _exists = false);
    _topicKey.currentState?.validate();
  }

  void _makeUpName() {
    final name = randomTopicName(ref.read(randomProvider));
    _topic.value = TextEditingValue(
      text: name,
      selection: TextSelection.collapsed(offset: name.length),
    );
    _topicEdited();
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
    _checkTimer?.cancel();
    _server.removeListener(_serverChanged);
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

  /// What the server said about itself, in words about what that means for this topic.
  Widget _noteView(AppLocalizations l10n, _ServerNote note) {
    final error = note.error;
    if (error != null) {
      return _Callout(
        icon: Icons.cloud_off_outlined,
        text: '${apiErrorText(l10n, error, note.host)} ${l10n.addNoteOffline}',
        isWarning: true,
      );
    }
    final info = note.info!;
    return switch (info.defaultAccess) {
      'read-write' => _Callout(
        icon: Icons.public,
        text: l10n.addNoteOpen(note.host),
      ),
      'read-only' => _Callout(
        icon: Icons.visibility_outlined,
        text: l10n.addNoteReadOnly(note.host),
      ),
      _ => _Callout(
        icon: Icons.lock_outline,
        text: l10n.addNoteClosed(note.host),
      ),
    };
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final policy = ref.watch(serverPolicyProvider);
    final known = ref.watch(serversProvider).value ?? const <ServerRow>[];

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
            if (_lockedMismatch != null) ...[
              _Callout(
                icon: Icons.lock_outline,
                text: l10n.addLockedMismatch(_lockedMismatch!),
              ),
              const SizedBox(height: 16),
            ],
            if (policy.allowCustom) ...[
              TextFormField(
                controller: _server,
                keyboardType: TextInputType.url,
                autocorrect: false,
                textInputAction: TextInputAction.next,
                onChanged: (value) {
                  _serverEdited = true;
                  // A subscribe link pasted here fills in the topic too.
                  if (value.trim().toLowerCase().startsWith(
                    '${Brand.urlScheme.toLowerCase()}:',
                  )) {
                    _fromPaste(value);
                  }
                },
                decoration: InputDecoration(
                  labelText: l10n.addServerLabel,
                  hintText: l10n.addServerHint,
                  helperText: l10n.addServerHelper,
                  helperMaxLines: 2,
                  suffixIcon: known.isEmpty
                      ? null
                      : PopupMenuButton<String>(
                          tooltip: l10n.addServerPick,
                          icon: const Icon(Icons.arrow_drop_down),
                          onSelected: (url) {
                            _serverEdited = true;
                            _server.text = url;
                          },
                          itemBuilder: (context) => [
                            for (final row in known)
                              PopupMenuItem(
                                value: row.url,
                                child: Text(row.url),
                              ),
                          ],
                        ),
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
              key: _topicKey,
              controller: _topic,
              autovalidateMode: AutovalidateMode.onUserInteraction,
              autofocus: !policy.allowCustom || _server.text.isNotEmpty,
              autocorrect: false,
              textInputAction: TextInputAction.done,
              onFieldSubmitted: (_) => _submit(),
              onChanged: (value) {
                // A pasted subscribe link or topic address fills in the server too.
                if (value.contains('://')) _fromPaste(value);
                if (_exists) setState(() => _exists = false);
              },
              decoration: InputDecoration(
                labelText: l10n.addTopicLabel,
                helperText: l10n.addTopicHelper,
                suffixIcon: IconButton(
                  tooltip: l10n.addTopicRandom,
                  icon: const Icon(Icons.casino_outlined),
                  onPressed: _makeUpName,
                ),
              ),
              validator: (value) {
                final name = value?.trim() ?? '';
                if (name.isEmpty) return l10n.addErrorTopicRequired;
                if (!isValidTopic(name)) return l10n.addErrorTopicInvalid;
                if (_exists) return l10n.addErrorExists;
                return null;
              },
            ),
            if (_note != null) ...[
              const SizedBox(height: 12),
              _noteView(l10n, _note!),
            ],
            const SizedBox(height: 24),
            Align(
              alignment: Alignment.centerRight,
              child: Wrap(
                alignment: WrapAlignment.end,
                spacing: 8,
                runSpacing: 8,
                children: [
                  TextButton(
                    onPressed: () => _close(GoRouter.of(context)),
                    child: Text(l10n.cancel),
                  ),
                  FilledButton(
                    onPressed: _busy ? null : _submit,
                    child: Text(l10n.subscribe),
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

/// What a server answered when looked up, or why it did not.
class _ServerNote {
  const _ServerNote({required this.host, this.info, this.error});

  final String host;
  final ServerInfo? info;
  final ApiException? error;
}

/// A short message in the form with an icon, in a quiet box.
class _Callout extends StatelessWidget {
  const _Callout({
    required this.icon,
    required this.text,
    this.isWarning = false,
  });

  final IconData icon;
  final String text;
  final bool isWarning;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final color = isWarning
        ? scheme.errorContainer
        : scheme.surfaceContainerHighest;
    final onColor = isWarning
        ? scheme.onErrorContainer
        : scheme.onSurfaceVariant;
    return Semantics(
      liveRegion: true,
      container: true,
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: color,
          borderRadius: BorderRadius.circular(8),
        ),
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Icon(icon, size: 20, color: onColor),
              const SizedBox(width: 12),
              Expanded(
                child: Text(
                  text,
                  style: Theme.of(context).textTheme.bodyMedium
                      ?.copyWith(color: onColor),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
