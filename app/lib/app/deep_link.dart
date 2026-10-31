import '../data/subscribe_link.dart';

/// The route a link from outside the app opens, or null if it is not one of the app's links.
///
/// Only the app's own scheme counts here (`<urlScheme>://subscribe/<host>/<topic>`): this is
/// what a QR code scan, a tapped link or a command line hands over, and nothing else is read as
/// a request to subscribe.
String? routeForLink(String text, {String? scheme}) {
  final link = scheme == null
      ? SubscribeLink.parse(text, webAddress: false)
      : SubscribeLink.parse(text, scheme: scheme, webAddress: false);
  return link?.location;
}

/// The route for the first link among the program's [arguments], for a desktop app started
/// with one (`<program> <link>`, which is how the system hands it over once the scheme is
/// registered).
String? routeFromArguments(List<String> arguments, {String? scheme}) {
  for (final argument in arguments) {
    final route = routeForLink(argument, scheme: scheme);
    if (route != null) return route;
  }
  return null;
}
