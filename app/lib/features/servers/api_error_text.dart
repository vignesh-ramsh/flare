import '../../l10n/app_localizations.dart';
import '../../net/api_client.dart';

/// What to tell the user about a failed request to [server].
String apiErrorText(AppLocalizations l10n, ApiException error, String server) {
  return switch (error.failure) {
    ApiFailure.unreachable => l10n.serverErrorUnreachable(server),
    ApiFailure.timeout => l10n.serverErrorTimeout(server),
    ApiFailure.certificate => l10n.serverErrorCertificate(server),
    ApiFailure.notSupported => l10n.serverErrorNotSupported(server),
    ApiFailure.badCredentials => l10n.signInBadCredentials,
    ApiFailure.needsSignIn => l10n.sendNeedsSignIn(server),
    ApiFailure.forbidden => l10n.sendForbidden,
    ApiFailure.tooLarge => l10n.sendTooLarge,
    ApiFailure.throttled => l10n.signInThrottled,
    ApiFailure.server => l10n.serverErrorOther(server),
  };
}
