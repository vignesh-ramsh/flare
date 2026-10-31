# App design

The structure of the Flutter app: what screens it has, how they adapt to the window, what each platform
does differently, and the rules the code follows. It was agreed from a design canvas with a mock-up for
every platform; this page is the written version, and the code follows it. Architecture and the stack
are in [ARCHITECTURE.md §10](ARCHITECTURE.md#10-client-architecture-flutter); white-labelling is §12.

Where the app and this page disagree, one of them is wrong: change the page in the same pull request.

## Decisions

These were proposed, reviewed against the mock-ups, and confirmed.

1. **Three destinations: Topics, Inbox, Settings.** Servers are not a destination. They are a section of
   Settings and a row of filter chips on Topics. Fewer destinations keep the phone's bottom bar usable
   and the desktop sidebar short.
2. **One set of Material 3 widgets, with per-platform tokens and window chrome.** Not four UI kits. iOS
   keeps large titles and a tab bar, Windows/macOS/Linux get their own title bars and tray, and each uses
   its own typeface and corner radius, but they are the same widgets with different theme extensions.
   This is the choice that keeps six targets maintainable by a small team.
3. **Delivery.** Android: Google push by default, UnifiedPush and Instant delivery are opt-in. iOS:
   Apple push only. Web: Web Push only. Desktop: a connection held while the app runs, which starts
   at login and hides to the tray or menu bar when the window is closed.
4. **History: 30 days kept on the device.** The server keeps messages for a day by default, so the
   device's copy is what makes the app useful offline. Settings can change it and clear it.
5. **Messages are cards, not chat bubbles.** A message has a priority, a title, text, tags and a link,
   which a bubble can't show. Only the user's own replies are bubbles.

6. **Plain `http://` servers are allowed everywhere**, as in ntfy: many people self-host on a home network
   with no certificate. An address typed without a scheme means https, and every example and placeholder in
   the app shows https. The price is that a token or password sent to an http server crosses the network in
   the clear, so when sign-in is built (step 4) the app must warn before sending one over http. Android sets
   `usesCleartextTraffic`, iOS sets `NSAllowsArbitraryLoads` (App Review may ask why: the servers are chosen
   by the user).

What is left out because the server does not support it yet: action buttons, attachments and images,
Markdown, delayed delivery, end-to-end encryption. The screens leave room for them; nothing is drawn that
the server would refuse.

## Window size classes

The layout follows the window width in logical pixels, not the device. The classes are Material 3's.

| Class | Width | Navigation | Panes |
|---|---|---|---|
| Compact | under 600 | Bottom bar | One. A topic's thread opens over the list, with a back arrow. Subscribe is a floating button and opens a bottom sheet |
| Medium | 600 to 839 | Navigation rail | One at a time, as Compact, but with the rail |
| Expanded | 840 to 1199 | Navigation rail | Topic list (360) and thread side by side. Subscribe is a dialog |
| Large | 1200 and up | Sidebar (280): servers and their topics as a tree, Inbox, Settings | Thread (capped at 720 for reading) and a details pane (320) that can be hidden |

The same route is a page in one class, a pane in another, a sheet or a dialog in a third; the route never
changes. Desktop windows are Large (or Expanded when narrowed), phones Compact, tablets Medium or
Expanded. Pointer targets are 40 dp when a mouse is present and 48 dp for touch.

## Routes

One route tree (go_router) for every platform. `:server` is the server's host, `:name` the topic.

| Route | Screen | Compact | Wide |
|---|---|---|---|
| `/` | Topics | page, bottom bar | list pane, or sidebar tree |
| `/topic/:server/:name` | Thread | page | main pane |
| `/topic/:server/:name/info` | Topic info | page | details pane |
| `/add` | Subscribe | bottom sheet | dialog |
| `/add-server` | Add server | bottom sheet | dialog |
| `/signin/:id` | Sign in to a server | bottom sheet | dialog |
| `/inbox` | Inbox | page | list pane |
| `/inbox/:id` | Message | page | details pane |
| `/search?q=` | Search | full screen | field in the title bar |
| `/settings` | Settings | page | pane |
| `/settings/servers` | Servers | page | pane |
| `/settings/servers/:id` | Server and account | page | details pane |
| `/settings/notifications` | Notifications | page | details pane |
| `/settings/appearance` | Appearance | page | details pane |
| `/settings/data` | Data and history | page | details pane |
| `/settings/desktop` | Desktop (Windows, macOS, Linux only) | not shown | pane |
| `/settings/about` | About | page | details pane |
| `/welcome` | Welcome (first run) | full screen | centred card |
| `/welcome/server` | Choose a server | full screen | centred card |
| `/welcome/account?server=ID` | Sign in (optional) | full screen | centred card |
| `/welcome/notifications` | Allow notifications | full screen | centred card |
| `/subscribe/:host/:topic` | Subscribe link or QR code | opens the Subscribe sheet, filled in | opens the dialog |

A notification tap, a QR code and a subscribe link all end at one of these routes with the topic and
server in the path. The first run is shown until a server is chosen, and is skipped when the brand locks
the app to one server.

## What each platform does

| | Android | iOS | Web | Windows | macOS | Linux |
|---|---|---|---|---|---|---|
| Navigation | Bottom bar, rail | Tab bar, large titles | By width | Sidebar | Sidebar, toolbar | Sidebar in a header-bar window |
| Window chrome | Edge to edge | Safe areas, swipe back | Browser or installed | Caption buttons right | Traffic lights left | Close button |
| Notifications | A channel per priority, inline reply | Time Sensitive for 4 and 5 | Web Push | Toast, Action Center | Banner, Notification Center | Desktop notification over D-Bus |
| Background delivery | Google push, UnifiedPush, Instant delivery service | Apple push; the extension fetches the text | Web Push | A connection while the app runs | A connection while the app runs | A connection; the command line can run as a service |
| Tray or menu bar | none | none | none | Tray icon and flyout | Menu bar icon and popover | Tray indicator and menu |
| Typeface | Roboto | SF Pro | Roboto | Segoe UI Variable | SF Pro | the desktop's own |

Android ships two flavours: `play`, with Google push, and `foss`, with no Google libraries, so it can go
on F-Droid. The web app cannot hold a connection in the background, so it has no Instant delivery, and
on iPhone it gets Web Push only once added to the Home Screen.

## Priority

Priority is shown with a word and a glyph, never colour alone. It also decides how a notification is
delivered on each platform.

| Priority | Shown as | Android | iOS | Desktop |
|---|---|---|---|---|
| 1 Min | Min | Min importance, silent | passive | list only |
| 2 Low | Low | Low importance | passive | toast, silent |
| 3 Default | no label | Default importance | active | toast and sound |
| 4 High | High | Heads-up | Time Sensitive | stays until dismissed |
| 5 Urgent | Urgent | Heads-up, long vibration | Time Sensitive | persistent, repeated sound |

## Look

Colours are Material 3 roles generated from the brand colour (`Brand.primaryColor`), in light and dark.
The raw brand colour appears only in the logo and the unread badge, always with text whose colour is
chosen for contrast; every other surface uses a role, and text keeps 4.5 to 1. The type scale is
Material's. Spacing is a 4 dp grid; corners are 8 on fields and chips, 12 on cards, 16 on sheets, 28 on
dialogs.

White-label: the name, colour, icon, locked server, links and URL scheme all come from `Brand`
(generated from `brand.yaml`). A build locked to one server hides Add server and the server chips. No
file under `app/lib` or `app/l10n` outside `lib/brand` may contain the project name; `make brand-check`
fails the build if one does.

## Data rules

- **The UI reads only the local database.** Connections and push write into it. A screen never waits on
  the network, and works offline.
- **A message is identified by its server and ID.** The ID sorts by time. Receiving one twice, by push
  and by catch-up, stores it once.
- **A cursor per server and topic** holds the ID of the last message received. The connection resumes
  with it (`since`), so nothing is missed or repeated.
- **Unread** is a message with an ID after the topic's read marker. Opening a thread at the newest
  message moves the marker.
- **Muted** topics keep receiving and storing, and notify nobody. **Minimum priority** drops nothing
  either; it only decides whether a notification is shown.
- **Credentials** (tokens, device secrets) live in the platform's secure storage, never in the database.

## Not yet decided

- A tablet-specific layout for very wide Medium windows (a foldable half-open) beyond the rules above.
- Whether Inbox should group by day or by topic by default.
- Reply from a notification on every platform that allows it, and what a failed reply looks like.

## Build order

1. **Skeleton** (done, see below): brand wiring, theme, router, adaptive shell, local database, Topics.
2. **Connection** (done, see below): one WebSocket per server, catch-up from the cursor, backoff, status. The REST client comes with step 4, when sign-in needs it.
3. **Thread and Subscribe**: reading and replying, the Subscribe sheet and dialog, deep links.
4. **First run, Servers, Settings**: sign-in, tokens, the delivery choice.
5. **Notifications**: the desktop tray and notifications (done, see below), then Android channels and service, the iOS extension, Web Push.
6. **Inbox and Search**, then polish: accessibility pass, translations, store listings.

## As built: the skeleton

What exists, so the page and the code agree.

- **Routes built:** `/`, `/topic/:server/:name`, `/add`, `/inbox`, `/settings`, `/settings/servers` and
  `/settings/about`. The rest of the table (topic info, a single message, search, the other settings pages,
  the first-run flow and subscribe links) come with the steps that need them. A link straight to `/add`
  opens the sheet with nothing under it, and closing it goes to Topics.
- **Subscribe saves the subscription in the local database.** It checks the server address and the topic
  name; the connection (below) then follows it. The Thread, Inbox and unread counts are driven by what the
  database holds.
- **Reading:** a thread that is on screen moves the topic's read marker to its newest message, so unread
  counts clear when it is opened and again when a message arrives while it is open.
- **Layouts:** the four window classes are built and tested at 390, 700, 1000 and 1400 dp, in light and
  dark, with large text. The details pane (Large) shows the server, the message count and whether the topic
  is muted, and can be hidden. The thread is capped at 720 dp in every class.
- **Message text** is plain text in cards, selectable as one area in a thread, so a screen reader sees
  cards rather than hundreds of tiny controls.
- **Target size** follows the platform (40 dp on Windows, macOS and Linux, 48 dp elsewhere), not whether a
  mouse is attached.
- **Data location:** the database is in the application-support folder, not the user's Documents.
- **Native projects:** every platform takes its name and identifier from the brand output and allows
  plain http (decision 6); CI builds each one and checks the result (see [app/README.md](../app/README.md)). The URL scheme is defined for
  Android but no platform registers it yet; that comes with deep links in step 3. macOS needed the
  `network.client` entitlement for the sandboxed app to reach any server, and Android's main manifest
  needed the INTERNET permission, which release builds lacked.

## As built: the connection

`lib/net` follows the subscriptions in the database over the network and writes what arrives into it. The
screens never touch it.

- **One WebSocket per server** (`/v1/ws`), held only while the server has topics to follow, and opened
  with the saved token as an `Authorization` header (there is no sign-in screen yet, so every connection
  is anonymous until the user signs in, see below). The app pings every 20 seconds, so a connection that died silently is noticed.
- **Catching up:** on every connect the app subscribes to each topic with `since` set to the topic's
  cursor, so it gets what it missed and nothing twice. A topic with no cursor yet starts from `all`: what
  the server still holds, a day by default, so a new subscription is not empty. Those messages count as
  unread. A message that arrives twice (live and in the backlog) is stored once.
- **Order:** messages are stored one at a time as they arrive, and a dropped connection finishes storing
  what it had before reconnecting, so the cursor never moves past a message that was not stored.
- **Retrying:** after a failure the wait doubles from 1 second up to 60, with half of each wait random, so
  a restarted server is not hit by every app at once. A connection that lasted 10 seconds counts as
  healthy, and the next retry is quick again; one that drops straight away keeps the wait growing.
  Returning to the app skips the wait.
- **What the server says no to:**

  | Server's answer | The app |
  |---|---|
  | 401 when connecting, a close with status 1008, or error 40102 | Stops retrying and says the saved sign-in was not accepted, until the login changes |
  | Error 40101 or 40301 on a topic | Keeps the connection, marks that topic, and says in the thread to sign in or that there is no access; tries it again on the next connection |
  | Error 50002 (too slow) | Follows the topic again from the cursor after a second |
  | Anything else, or no answer | Retries with the wait above |

- **What the user sees:** a red bar with "Try now" when a server cannot be reached or refused the login:
  at the top of the topic list, in the sidebar of the large layout, and in a thread when the list is hidden.
  A topic the server refused shows its own bar in the thread. Nothing is shown while connecting.
- **History:** the app deletes messages older than 30 days at launch. The cursor is kept, so they are
  not fetched again.
- **Not built:** reconnecting when the network changes (the ping and returning to the app cover it for
  now), a connection while the app is closed (step 5), and sending messages (step 3).

## As built: sign-in

- **First run** (`/welcome`, three pages): what the app is, choose a server, sign in or skip. It is shown
  while there is no server, and only then: removing the last server brings it back. A server is chosen by
  typing its address, which the app checks with `GET /v1/config` before adding it, saying what it found
  ("Found Acme Alerts. You need to sign in to read its topics.") or why it could not (no answer, too slow, a
  certificate it does not accept, or an address that is not a server this app can use). A build locked to one
  server always has it, skips these pages, and signs in later.
- **Signing in** takes a user name and password, sent once (`POST /v1/auth/login`, Basic) for a token. What is
  kept is the token, labelled with the app and the platform so it shows in the user's list of devices; the
  password is not stored. The connection restarts at once with the new login, so private topics open without
  waiting. On a plain-http server the form warns that the password will cross the network unencrypted and the
  button reads "Sign in anyway" (decision 6).
- **Where tokens live:** the platform's secure storage (Keychain on iOS and macOS, an encrypted store on
  Android, the Credential Locker on Windows, the Secret Service on Linux), keyed by the server's address, never
  in the database. Where the secure storage cannot be used, or never answers (a Linux desktop with no
  keyring), the sign-in is kept in memory for the run and the server's page says it will not survive closing
  the app. Every call to the secure storage has a 5 second limit, so a stuck keyring cannot hold the app up.
- **Signing out** revokes the token on the server (`DELETE /v1/account/tokens/{id}`) and forgets it here. If
  the server cannot be reached the sign-out still happens here and the app says the token is not revoked yet.
  Removing a server signs out of it first, and deletes its topics and messages from the device.
- **Where it shows up:** Settings > Servers lists each server with "Signed in as ..." or "Not signed in"; a
  server's page has its address, the state of its connection, the account (sign in or out) and Remove. The red
  bar for a refused login, and for a topic that needs a sign-in (error 40101), has a Sign in button; a topic
  that is simply forbidden (40301) does not, since signing in again would not change it.
- **Startup:** the app reads the saved sign-ins, makes sure a locked build has its server, and waits for the
  servers before the first frame so the first-run check does not flash the wrong page; then it follows the
  subscriptions.
- **Not built:** the REST client has `config`, `login` and `revokeToken` only; changing a password,
  listing the account's tokens, and creating accounts are done on the server's command line.

## As built: desktop notifications

Windows, macOS and Linux hold their own connection, so the app announces what arrives. Phones are
woken by the system's push service instead (not built yet); none of this runs there.

- **What becomes a notification** (`decideLevel`, one pure function). In this order a message stays in the
  list only if: it is history fetched when a topic was first subscribed to; notifications are off or paused;
  the topic is muted; it is below the topic's minimum priority; it is priority 1 (min); or the window is
  focused on that topic's thread (the user is reading it). Otherwise priority 2 is a notification with no sound,
  3 a normal one, and 4 and 5 stay until dismissed. A sound plays only if the user allows it.
- **History is not news.** On a topic's first fetch the app stores what the server holds without a
  notification each; a message older than 10 seconds when it arrives is history, one just published is not.
  Once a topic has a cursor, everything after it is news, even if the app was away for hours.
- **A burst is one notification.** Messages that arrive close together on a topic (what piled up while the app
  was away) are announced one by one up to three, and as "N new messages" beyond that, at the most prominent
  level among them so an urgent one is not buried.
- **What it shows:** the title (or the topic), with the priority word before it for anything not default
  ("High · Disk"), the text, and "topic on server". Settings can keep the text out ("New message" only), for a
  lock screen or a shared screen.
- **Choices:** Settings > Notifications (show, sound, show the text, pause for an hour, and on a desktop whether
  to keep running in the tray), and per topic from the bell in the thread (mute, minimum priority). They are
  kept in the database (schema version 2, with a migration).
- **Tapping a notification** opens its thread and brings the window forward.
- **The tray** (menu bar on macOS, notification area on Windows, a StatusNotifierItem on Linux): its menu opens
  the app, lists the four latest messages (choosing one opens its thread), pauses or resumes notifications for
  an hour, and quits; its tooltip counts unread messages. Closing the window hides it to the tray only when the
  user wants that **and a tray is there**; on Linux that is checked by looking for the StatusNotifierWatcher on
  the session bus. With no tray, closing the window quits, so the app is never left running unseen.
- **Platform differences:** Linux sends the notification over D-Bus (critical urgency and no timeout for 4 and
  5); macOS uses Notification Center with time-sensitive for 4 and 5 and a subtitle; Windows uses toasts with the
  app's identity from the brand (`Brand.windowsIdentity`, and a GUID derived from it).
- **Not built:** a repeated sound for priority 5, "Time Sensitive" breaking through Focus on macOS beyond what
  the system grants, starting at login, notification actions (Open, Mute), and any of it on Android and iOS.
  Windows and macOS are built in CI but have not been run, so their toasts, tray and permission prompt are
  unverified; Linux is verified against a real notification daemon.

