import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/models.dart';

/// The topic whose thread is on screen, or null. A message for it needs no notification while
/// the window is focused.
class ViewedTopic extends Notifier<TopicRef?> {
  @override
  TopicRef? build() => null;

  void show(TopicRef topic) => state = topic;

  /// Forgets [topic] if it is still the one shown (another thread may have opened since).
  void leave(TopicRef topic) {
    if (state == topic) state = null;
  }
}

final viewedTopicProvider = NotifierProvider<ViewedTopic, TopicRef?>(
  ViewedTopic.new,
);

/// Whether the app's window has the user's attention.
class WindowFocus extends Notifier<bool> {
  @override
  bool build() => true;

  void set(bool focused) => state = focused;
}

final windowFocusProvider = NotifierProvider<WindowFocus, bool>(
  WindowFocus.new,
);
