/// The route of a topic's thread.
String topicLocation(String host, String name) =>
    '/topic/${Uri.encodeComponent(host)}/${Uri.encodeComponent(name)}';

/// The route of a topic's info page: facts, sharing and unsubscribing.
String topicInfoLocation(String host, String name) =>
    '${topicLocation(host, name)}/info';
