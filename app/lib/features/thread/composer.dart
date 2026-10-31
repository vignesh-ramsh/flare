import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/theme.dart';
import '../../data/models.dart';
import '../../l10n/app_localizations.dart';
import '../../net/api_client.dart';
import '../../net/publisher.dart';
import '../servers/api_error_text.dart';

/// Writing a message to the topic: text, and optionally a title and a priority.
class Composer extends ConsumerStatefulWidget {
  const Composer({required this.topic, super.key});

  final TopicSummary topic;

  @override
  ConsumerState<Composer> createState() => _ComposerState();
}

class _ComposerState extends ConsumerState<Composer> {
  final _text = TextEditingController();
  final _title = TextEditingController();
  final _focus = FocusNode();
  bool _options = false;
  int _priority = 3;
  bool _sending = false;
  ApiException? _error;

  @override
  void initState() {
    super.initState();
    _text.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _text.dispose();
    _title.dispose();
    _focus.dispose();
    super.dispose();
  }

  bool get _canSend => !_sending && _text.text.trim().isNotEmpty;

  Future<void> _send() async {
    if (!_canSend) return;
    setState(() {
      _sending = true;
      _error = null;
    });
    final topic = widget.topic;
    try {
      await ref
          .read(publisherProvider)
          .send(
            serverId: topic.serverId,
            topic: topic.name,
            text: _text.text.trim(),
            title: _title.text.trim(),
            priority: _priority == 3 ? null : _priority,
          );
      if (!mounted) return;
      // Sent: the box is empty again, and the options go back to what a message usually is.
      setState(() {
        _text.clear();
        _title.clear();
        _priority = 3;
        _sending = false;
      });
      _focus.requestFocus();
    } on ApiException catch (error) {
      if (!mounted) return;
      // Nothing is lost: what was typed stays, so the user can fix the cause and send again.
      setState(() {
        _sending = false;
        _error = error;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final desktop = isDesktopPlatform(defaultTargetPlatform);
    final words = {
      1: l10n.priorityMin,
      2: l10n.priorityLow,
      3: l10n.priorityDefault,
      4: l10n.priorityHigh,
      5: l10n.priorityUrgent,
    };

    return Material(
      color: theme.colorScheme.surfaceContainer,
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 720),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (_error != null)
                    _ErrorLine(error: _error!, topic: widget.topic),
                  if (_options) ...[
                    Row(
                      children: [
                        Expanded(
                          child: TextField(
                            controller: _title,
                            maxLength: 250,
                            decoration: InputDecoration(
                              labelText: l10n.composerTitle,
                              counterText: '',
                              isDense: true,
                            ),
                          ),
                        ),
                        const SizedBox(width: 12),
                        SizedBox(
                          width: 168,
                          child: DropdownButtonFormField<int>(
                            initialValue: _priority,
                            isDense: true,
                            // A big text size shortens the word with an ellipsis; it never overflows.
                            isExpanded: true,
                            decoration: InputDecoration(
                              labelText: l10n.composerPriority,
                              isDense: true,
                            ),
                            items: [
                              for (final entry in words.entries)
                                DropdownMenuItem(
                                  value: entry.key,
                                  child: Text(entry.value),
                                ),
                            ],
                            onChanged: (value) =>
                                setState(() => _priority = value ?? 3),
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 8),
                  ],
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.end,
                    children: [
                      IconButton(
                        tooltip: l10n.composerOptions,
                        isSelected: _options,
                        icon: const Icon(Icons.tune),
                        onPressed: () => setState(() => _options = !_options),
                      ),
                      Expanded(
                        child: CallbackShortcuts(
                          // On a desktop, Enter sends and Shift+Enter starts a new line; on a
                          // phone the keyboard's Enter is a new line and the button sends.
                          bindings: desktop
                              ? {
                                  const SingleActivator(
                                    LogicalKeyboardKey.enter,
                                  ): _send,
                                }
                              : const {},
                          child: TextField(
                            controller: _text,
                            focusNode: _focus,
                            minLines: 1,
                            maxLines: 5,
                            textCapitalization: TextCapitalization.sentences,
                            keyboardType: TextInputType.multiline,
                            decoration: InputDecoration(
                              hintText: l10n.composerHint,
                              isDense: true,
                            ),
                          ),
                        ),
                      ),
                      const SizedBox(width: 4),
                      IconButton.filled(
                        tooltip: l10n.composerSend,
                        icon: const Icon(Icons.send),
                        onPressed: _canSend ? _send : null,
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _ErrorLine extends ConsumerWidget {
  const _ErrorLine({required this.error, required this.topic});

  final ApiException error;
  final TopicSummary topic;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final scheme = Theme.of(context).colorScheme;
    return Semantics(
      liveRegion: true,
      container: true,
      child: Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Row(
          children: [
            Icon(Icons.error_outline, size: 18, color: scheme.error),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                apiErrorText(l10n, error, topic.host),
                style: Theme.of(context).textTheme.bodyMedium
                    ?.copyWith(color: scheme.error),
              ),
            ),
            if (error.failure == ApiFailure.needsSignIn)
              TextButton(
                onPressed: () => context.push('/signin/${topic.serverId}'),
                child: Text(l10n.bannerSignIn),
              ),
          ],
        ),
      ),
    );
  }
}
