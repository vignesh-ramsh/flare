import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../data/models.dart';
import '../features/add/add_screen.dart';
import '../features/inbox/inbox_screen.dart';
import '../features/servers/server_modals.dart';
import '../features/settings/notifications_screen.dart';
import '../features/settings/server_detail_screen.dart';
import '../features/settings/about_screen.dart';
import '../features/settings/servers_screen.dart';
import '../features/settings/settings_screen.dart';
import '../features/topics/topics_page.dart';
import '../features/welcome/welcome_pages.dart';
import 'modal_pages.dart';
import 'shell.dart';

/// The app's routes, the same on every platform (docs/APP-DESIGN.md has the table).
/// The shell around them is a bottom bar, a rail or a sidebar, by window width.
GoRouter createRouter({String initialLocation = '/', FirstRunGate? firstRun}) {
  final rootKey = GlobalKey<NavigatorState>(debugLabel: 'root');

  return GoRouter(
    navigatorKey: rootKey,
    initialLocation: initialLocation,
    refreshListenable: firstRun,
    // Until a server is chosen, the first-run pages are all there is.
    redirect: firstRun == null
        ? null
        : (context, state) =>
              firstRun.needed && !state.matchedLocation.startsWith('/welcome')
              ? '/welcome'
              : null,
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
        pageBuilder: (context, state) =>
            adaptiveModalPage(context, state, const AddScreen()),
      ),
    ],
  );
}

/// Says whether the first-run pages must be shown: while there is no server to follow.
class FirstRunGate extends ChangeNotifier {
  bool _needed = false;

  bool get needed => _needed;

  void update(bool needed) {
    if (_needed == needed) return;
    _needed = needed;
    notifyListeners();
  }
}
