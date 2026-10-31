import 'package:drift/drift.dart';
import 'package:drift_flutter/drift_flutter.dart';
import 'package:path_provider/path_provider.dart';

import 'database.dart';

/// Opens the on-device database. Messages, subscriptions and cursors live here;
/// credentials do not (they go to the platform's secure storage).
AppDatabase openAppDatabase() => AppDatabase(
  driftDatabase(
    name: 'messages',
    // App data, not the user's Documents folder (which a minimal Linux desktop may not have).
    native: const DriftNativeOptions(
      databaseDirectory: getApplicationSupportDirectory,
    ),
  ),
);

/// For tests and tools that bring their own connection.
AppDatabase openAppDatabaseWith(QueryExecutor executor) =>
    AppDatabase(executor);
