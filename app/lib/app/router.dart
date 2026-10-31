import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../brand/brand.g.dart';
import '../data/models.dart';
import '../data/subscribe_link.dart';
import '../features/add/add_screen.dart';
import '../features/inbox/inbox_screen.dart';
import '../features/servers/server_modals.dart';
import '../features/settings/appearance_screen.dart';
import '../features/settings/notifications_screen.dart';
import '../features/settings/server_detail_screen.dart';
import '../features/settings/about_screen.dart';
import '../features/settings/servers_screen.dart';
import '../features/settings/settings_screen.dart';
import '../features/thread/topic_info_page.dart';
import '../features/topics/topics_page.dart';
import '../features/welcome/welcome_pages.dart';
import 'deep_link.dart';
import 'modal_pages.dart';
import 'shell.dart';
import 'window_class.dart';

/// The app's routes, the same on every platform (docs/APP-DESIGN.md has the table).
/// The shell around them is a bottom bar, a rail or a sidebar, by window width.
GoRouter createRouter({String initialLocation = '/', FirstRunGate? firstRun}) {
  final rootKey = GlobalKey<NavigatorState>(debugLabel: 'root');

  return GoRouter(
    navigatorKey: rootKey,
    initialLocation: initialLocation,
    refreshListenable: firstRun,
    redirect: (context, state) {
      // A link from outside, with the app's own scheme: the route for it, or the topics for one
      // that is not a subscribe link.
      if (state.uri.scheme == Brand.urlScheme.toLowerCase()) {
        return routeForLink(state.uri.toString()) ?? '/';
      }
      // Until a server is chosen, the first-run pages are all there is. A subscribe link names
      // its server, so it can be answered at once.
      if (firstRun != null) {
        final path = state.matchedLocation;
        if (firstRun.needed) {
          if (!path.startsWith('/welcome') && !path.startsWith('/subscribe')) {
            // Remember where the user was going: the gate learns of a new server a moment after
            // it is added, so a link that subscribes can be sent here just before it clears.
            firstRun.blocked = state.uri.toString();
            return '/welcome';
          }
        } else if (path == '/welcome') {
          // The first page is never needed once there is a server: carry on to where the user
          // was going, or the topics.
          final resume = firstRun.blocked;
          firstRun.blocked = null;
          return resume ?? '/';
        }
      }
      return null;
    },
    routes: [
      ShellRoute(
        builder: (context, state, child) =>
            AppShell(location: state.uri.path, child: child),
        routes: [
          GoRoute(
            path: '/',
            builder: (context, state) => const TopicsPage(),
            routes: [
              GoRoute(
                path: 'topic/:server/:name',
                builder: (context, state) {
                  final TopicRef topic = (
                    host: state.pathParameters['server']!,
                    name: state.pathParameters['name']!,
                  );
                  return TopicsPage(selected: topic);
                },
                routes: [
                  GoRoute(
                    path: 'info',
                    builder: (context, state) {
                      final TopicRef topic = (
                        host: state.pathParameters['server']!,
                        name: state.pathParameters['name']!,
                      );
                      // The large layout already shows the info beside the thread.
                      return context.windowClass == WindowClass.large
                          ? TopicsPage(selected: topic)
                          : TopicInfoPage(topic: topic);
                    },
                  ),
                ],
              ),
            ],
          ),
          GoRoute(
            path: '/inbox',
            builder: (context, state) => const InboxScreen(),
          ),
          GoRoute(
            path: '/settings',
            builder: (context, state) => const SettingsScreen(),
            routes: [
              GoRoute(
                path: 'servers',
                builder: (context, state) => const ServersScreen(),
                routes: [
                  GoRoute(
                    path: ':id',
                    builder: (context, state) => ServerDetailScreen(
                      serverId: int.tryParse(state.pathParameters['id']!) ?? 0,
                    ),
                  ),
                ],
              ),
              GoRoute(
                path: 'notifications',
                builder: (context, state) => const NotificationsScreen(),
              ),
              GoRoute(
                path: 'appearance',
                builder: (context, state) => const AppearanceScreen(),
              ),
              GoRoute(
                path: 'about',
                builder: (context, state) => const AboutScreen(),
              ),
            ],
          ),
        ],
      ),
      GoRoute(
        path: '/welcome',
        builder: (context, state) => const WelcomePage(),
      ),
      GoRoute(
        path: '/welcome/server',
        builder: (context, state) => const WelcomeServerPage(),
      ),
      GoRoute(
        path: '/welcome/account',
        builder: (context, state) => WelcomeAccountPage(
          serverId:
              int.tryParse(state.uri.queryParameters['server'] ?? '') ?? 0,
        ),
      ),
      GoRoute(
        path: '/signin/:id',
        parentNavigatorKey: rootKey,
        pageBuilder: (context, state) => adaptiveModalPage(
          context,
          state,
          SignInModal(serverId: int.tryParse(state.pathParameters['id']!) ?? 0),
        ),
      ),
      GoRoute(
        path: '/add-server',
        parentNavigatorKey: rootKey,
        pageBuilder: (context, state) =>
            adaptiveModalPage(context, state, const AddServerModal()),
      ),
      GoRoute(
        path: '/add',
        parentNavigatorKey: rootKey,
        pageBuilder: (context, state) => adaptiveModalPage(
          context,
          state,
          AddScreen(
            server: state.uri.queryParameters['server'],
            topic: state.uri.queryParameters['topic'],
          ),
          // A different link, opened while the form is showing, starts the form again.
          key: ValueKey(state.uri.toString()),
        ),
      ),
      // A subscribe link or QR code: the form, filled in, for the user to confirm.
      GoRoute(
        path: '/subscribe/:host/:topic',
        parentNavigatorKey: rootKey,
        pageBuilder: (context, state) {
          final link = SubscribeLink.parse(
            '${Brand.urlScheme}://subscribe/'
            '${Uri.encodeComponent(state.pathParameters['host']!)}/'
            '${Uri.encodeComponent(state.pathParameters['topic']!)}'
            '${state.uri.queryParameters['scheme'] == 'http' ? '?scheme=http' : ''}',
          );
          return adaptiveModalPage(
            context,
            state,
            // A link that is not valid opens the plain form.
            AddScreen(server: link?.server.url, topic: link?.topic),
            key: ValueKey(state.uri.toString()),
          );
        },
      ),
    ],
  );
}

/// Says whether the first-run pages must be shown: while there is no server to follow.
class FirstRunGate extends ChangeNotifier {
  bool _needed = false;

  /// Where the user was going when the first-run pages stopped them, to go there when there is
  /// a server.
  String? blocked;

  bool get needed => _needed;

  void update(bool needed) {
    if (_needed == needed) return;
    _needed = needed;
    notifyListeners();
  }
}
