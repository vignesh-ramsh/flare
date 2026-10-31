import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../app/routes.dart';
import '../../data/models.dart';
import '../../l10n/app_localizations.dart';
import 'topic_info_view.dart';

/// A topic's info as a page of its own, for windows too narrow for the details pane.
class TopicInfoPage extends StatelessWidget {
  const TopicInfoPage({required this.topic, super.key});

  final TopicRef topic;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    return Scaffold(
      appBar: AppBar(
        leading: BackButton(
          onPressed: () => context.canPop()
              ? context.pop()
              : context.go(topicLocation(topic.host, topic.name)),
        ),
        title: Text(l10n.topicInfoTitle),
      ),
      body: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(16),
            child: SelectionArea(child: TopicInfoView(topic: topic)),
          ),
        ),
      ),
    );
  }
}
