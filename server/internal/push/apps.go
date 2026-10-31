package push

import (
	"fmt"
	"net/http"
	"os"
)

// App holds the push credentials of one app: the files an operator downloads from
// the Firebase console and the Apple Developer account.
type App struct {
	// AppID is the Android application ID and Apple bundle ID.
	AppID string
	// FCMCredentials is the path of a Firebase service account key (JSON).
	FCMCredentials string
	// APNsKey is the path of an Apple .p8 key, with its ID and the team's.
	APNsKey, APNsKeyID, APNsTeamID string
	// APNsSandbox uses Apple's development servers.
	APNsSandbox bool
}

// AddApp reads the app's credential files and registers the senders they give.
// An error names the app and the file, because the likely cause is a wrong path or
// the wrong kind of file, and the operator is looking at a configuration file.
func AddApp(r *Registry, app App, client *http.Client) error {
	if app.FCMCredentials != "" {
		creds, err := os.ReadFile(app.FCMCredentials)
		if err != nil {
			return fmt.Errorf("push.apps %s: read fcm-credentials: %w", app.AppID, err)
		}
		sender, err := NewFCM(creds, client)
		if err != nil {
			return fmt.Errorf("push.apps %s: fcm-credentials %s: %w", app.AppID, app.FCMCredentials, err)
		}
		r.AddApp(FCM, app.AppID, sender)
	}
	if app.APNsKey != "" {
		key, err := os.ReadFile(app.APNsKey)
		if err != nil {
			return fmt.Errorf("push.apps %s: read apns-key: %w", app.AppID, err)
		}
		sender, err := NewAPNs(key, app.APNsKeyID, app.APNsTeamID, app.AppID, app.APNsSandbox, client)
		if err != nil {
			return fmt.Errorf("push.apps %s: apns-key %s: %w", app.AppID, app.APNsKey, err)
		}
		r.AddApp(APNs, app.AppID, sender)
	}
	return nil
}
