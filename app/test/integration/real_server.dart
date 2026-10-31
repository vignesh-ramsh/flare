import 'dart:async';
import 'dart:convert';
import 'dart:io';

/// The path of the `flare` binary, from the environment (`make server` builds it).
String? get serverBinary => Platform.environment['FLARE_SERVER_BIN'];

const skipWithoutServer =
    'set FLARE_SERVER_BIN to the path of the flare binary (make server)';

/// A real `flare serve` process on a temporary database, for tests of the app against
/// the server it is written for.
class RealServer {
  RealServer._(this._bin, this._dir, this.port, this._env);

  final String _bin;
  final Directory _dir;
  final int port;
  final Map<String, String> _env;
  Process? _process;
  final StringBuffer log = StringBuffer();

  Uri get url => Uri.parse('http://127.0.0.1:$port');
  String get database => 'sqlite://${_dir.path}/flare.db';

  /// Starts a server. [env] adds settings such as `FLARE_AUTH_DEFAULT_ACCESS`.
  static Future<RealServer> start({Map<String, String> env = const {}}) async {
    final bin = serverBinary;
    if (bin == null) throw StateError(skipWithoutServer);
    final dir = await Directory.systemTemp.createTemp('flare-app-test');
    final probe = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final port = probe.port;
    await probe.close();
    final server = RealServer._(bin, dir, port, env);
    await server.restart();
    return server;
  }

  /// Starts the process, again if it was stopped. The database and port stay.
  Future<void> restart() async {
    final process = await Process.start(_bin, [
      'serve',
      '--listen',
      '127.0.0.1:$port',
      '--database',
      database,
      '--log-level',
      'warn',
    ], environment: _env);
    process.stdout.transform(utf8.decoder).listen(log.write);
    process.stderr.transform(utf8.decoder).listen(log.write);
    _process = process;
    await _waitUntilReady();
  }

  /// Stops it as `docker stop` does (SIGTERM), so it closes connections gracefully.
  Future<void> stop() async {
    final process = _process;
    if (process == null) return;
    _process = null;
    process.kill(ProcessSignal.sigterm);
    await process.exitCode.timeout(
      const Duration(seconds: 10),
      onTimeout: () {
        process.kill(ProcessSignal.sigkill);
        return -1;
      },
    );
  }

  Future<void> dispose() async {
    await stop();
    await _dir.delete(recursive: true);
  }

  Future<void> _waitUntilReady() async {
    final client = HttpClient();
    try {
      final deadline = DateTime.now().add(const Duration(seconds: 15));
      while (DateTime.now().isBefore(deadline)) {
        try {
          final request = await client.getUrl(url.replace(path: '/healthz'));
          final response = await request.close();
          await response.drain<void>();
          if (response.statusCode == 200) return;
        } on SocketException {
          // not listening yet
        }
        await Future<void>.delayed(const Duration(milliseconds: 50));
      }
      throw StateError('the server did not start:\n$log');
    } finally {
      client.close(force: true);
    }
  }

  /// Publishes with a plain HTTP request, as any script would. Returns the message's ID.
  Future<String> publish(
    String topic,
    String body, {
    String? title,
    int? priority,
    List<String>? tags,
    String? token,
  }) async {
    final client = HttpClient();
    try {
      final request = await client.postUrl(url.replace(path: '/$topic'));
      if (title != null) request.headers.set('Title', title);
      if (priority != null) request.headers.set('Priority', '$priority');
      if (tags != null) request.headers.set('Tags', tags.join(','));
      if (token != null) request.headers.set('Authorization', 'Bearer $token');
      request.write(body);
      final response = await request.close();
      final text = await utf8.decodeStream(response);
      if (response.statusCode != 200) {
        throw StateError('publish failed: ${response.statusCode} $text');
      }
      return (jsonDecode(text) as Map<String, dynamic>)['id'] as String;
    } finally {
      client.close(force: true);
    }
  }

  /// The password every test user is created with.
  static const testPassword = 'a long password for tests';

  /// Creates a user and an access token for them, with the server's own command line.
  Future<String> addUserWithToken(String name, {bool admin = false}) async {
    await addUser(name, admin: admin);
    final token = await Process.run(_bin, [
      'token',
      'add',
      '--database',
      database,
      name,
    ]);
    if (token.exitCode != 0) {
      throw StateError('token add failed: ${token.stdout} ${token.stderr}');
    }
    return (token.stdout as String).trim();
  }

  /// Creates a user, with the server's own command line.
  Future<void> addUser(String name, {bool admin = false}) async {
    final add = await Process.start(_bin, [
      'user',
      'add',
      if (admin) '--admin',
      '--database',
      database,
      name,
    ]);
    add.stdin.writeln(testPassword);
    await add.stdin.close();
    final addOut = await add.stdout.transform(utf8.decoder).join();
    final addErr = await add.stderr.transform(utf8.decoder).join();
    if (await add.exitCode != 0) {
      throw StateError('user add failed: $addOut $addErr');
    }
  }
}
