// The whole app on the real platform, against the real Flare server: first run, signing in
// over the real secure storage (which falls back where there is no keyring), subscribing,
// and receiving a message. Unlike the widget tests, nothing here is replaced, so it finds
// what only the real platform shows (a plugin that never answers, a missing library).
//
//   make server
//   HOME=$(mktemp -d) FLARE_SERVER_BIN=$PWD/server/bin/flare \
//     xvfb-run -a flutter test integration_test -d linux
//
// Run it with an empty HOME: the app keeps its database there.
import 'dart:async';

import 'package:app/features/thread/message_bubble.dart';
import 'package:app/main.dart' as app;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import '../test/integration/real_server.dart';
import 'helpers.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets(
    'first run, sign in, subscribe and receive a message',
    (tester) async {
      final server = await RealServer.start(
        env: {'FLARE_AUTH_DEFAULT_ACCESS': 'deny-all'},
      );
      addTearDown(server.dispose);
      final token = await server.addUserWithToken('alice', admin: true);

      unawaited(app.main());

      // First run: no server yet.
      await pumpUntil(tester, find.text('Get started'));
      await tester.tap(find.text('Get started'));
      await pumpUntil(tester, find.text('Choose a server'));

      await tester.enterText(
        find.widgetWithText(TextFormField, 'Server'),
        server.url.toString(),
      );
      await tester.tap(find.text('Check server'));
      await pumpUntil(tester, find.textContaining('Found'));
      expect(
        find.text('You need to sign in to read its topics.'),
        findsOneWidget,
      );
      await tester.tap(find.text('Continue'));

      // Sign in. The server is on plain http, so the app warns first.
      await pumpUntil(tester, find.textContaining('Sign in to'));
      expect(find.textContaining('plain http'), findsOneWidget);
      await tester.enterText(
        find.widgetWithText(TextFormField, 'User name'),
        'alice',
      );
      await tester.enterText(
        find.widgetWithText(TextFormField, 'Password'),
        RealServer.testPassword,
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Sign in anyway'));
      await pumpUntil(tester, find.text('No topics yet'));

      // Subscribe to a topic only a signed-in user may read.
      await tester.tap(find.widgetWithText(FilledButton, 'Subscribe').first);
      await pumpUntil(tester, find.widgetWithText(TextFormField, 'Topic'));
      await tester.enterText(
        find.widgetWithText(TextFormField, 'Topic'),
        'private',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Subscribe').last);
      await pumpUntil(
        tester,
        find.text('Nothing has been published to this topic yet.'),
      );

      // The sign-in is what lets the connection read it: a message arrives live.
      await server.publish(
        'private',
        'for signed-in users only',
        title: 'Hello',
        priority: 4,
        token: token,
      );
      await pumpUntil(tester, find.text('Hello'));
      expect(find.text('for signed-in users only'), findsOneWidget);
      expect(find.text('High'), findsOneWidget);

      // Reply from the app: the message goes to the real server over REST, shows as the user's
      // own, and the copy that comes back on the live connection does not make a second one.
      await tester.enterText(
        find.widgetWithText(TextField, 'Write a message'),
        'on my way',
      );
      // The button enables itself when there is text; give it a frame to do so before pressing.
      await tester.pump();
      await tester.tap(find.byTooltip('Send'));
      // A bubble, not the words still in the box: the box is where they are until it is sent.
      final sent = find.descendant(
        of: find.byType(MessageBubble),
        matching: find.text('on my way'),
      );
      try {
        await pumpUntil(tester, sent);
      } on TestFailure {
        final said = tester
            .widgetList<Text>(find.byType(Text))
            .map((t) => t.data)
            .join(' | ');
        throw TestFailure(
          'the reply never showed as a bubble. On screen: $said',
        );
      }
      await pumpFor(tester, const Duration(seconds: 2));
      expect(
        sent,
        findsOneWidget,
        reason: 'the live copy is not a second message',
      );
      final held = await server.stored('private', token: token);
      expect(held.map((m) => m['message']), [
        'for signed-in users only',
        'on my way',
      ], reason: 'the server holds what the app sent');
      // The box is empty again, ready for the next one.
      expect(
        tester
            .widget<TextField>(
              find.widgetWithText(TextField, 'Write a message'),
            )
            .controller!
            .text,
        isEmpty,
      );

      // Signing out closes the topic again.
      await tester.tap(find.text('Settings'));
      await pumpUntil(tester, find.text('Servers'));
      await tester.tap(find.text('Servers'));
      await pumpUntil(tester, find.textContaining('Signed in as alice'));
      await tester.tap(find.textContaining('Signed in as alice'));
      await pumpUntil(tester, find.widgetWithText(OutlinedButton, 'Sign out'));
      await tester.tap(find.widgetWithText(OutlinedButton, 'Sign out'));
      await pumpUntil(tester, find.widgetWithText(FilledButton, 'Sign out'));
      await tester.tap(find.widgetWithText(FilledButton, 'Sign out'));
      await pumpUntil(tester, find.text('Not signed in'));
    },
    timeout: const Timeout(Duration(minutes: 3)),
    skip: serverBinary == null,
  );
}
