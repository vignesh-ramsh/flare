import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../data/models.dart';
import '../features/add/add_screen.dart';
import '../features/inbox/inbox_screen.dart';
import '../features/settings/about_screen.dart';
import '../features/settings/servers_screen.dart';
import '../features/settings/settings_screen.dart';
import '../features/topics/topics_page.dart';
import 'modal_pages.dart';
import 'shell.dart';

/// The app's routes, the same on every platform (docs/APP-DESIGN.md has the table).
/// The shell around them is a bottom bar, a rail or a sidebar, by window width.
GoRouter createRouter({String initialLocation = '/'}) {
  final rootKey = GlobalKey<NavigatorState>(debugLabel: 'root');

  return GoRouter(
    navigatorKey: rootKey,
    initialLocation: initialLocation,
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
        path: '/add',
        parentNavigatorKey: rootKey,
        pageBuilder: (context, state) =>
            adaptiveModalPage(context, state, const AddScreen()),
      ),
    ],
  );
}
