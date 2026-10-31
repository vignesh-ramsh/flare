/// The route of a topic's thread.
String topicLocation(String host, String name) =>
    '/topic/${Uri.encodeComponent(host)}/${Uri.encodeComponent(name)}';
