import 'package:app/data/topics_repository.dart';
import 'package:app/net/connection_manager.dart';

/// A manager that records what it was asked instead of touching the network.
class SpyManager extends ConnectionManager {
  SpyManager(TopicsRepository topics)
    : super(topics: topics, factory: (_) => throw StateError('not used'));

  int reconnects = 0;
  final List<String> loginChanges = [];

  @override
  void reconnectAll() => reconnects++;

  @override
  void loginChanged(String url) => loginChanges.add(url);
}
