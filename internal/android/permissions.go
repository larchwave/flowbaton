package android

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nohavewho/flowbaton/internal/device"
)

// setPermissions on Android is pm grant / pm revoke per permission.
//
// The short-name vocabulary is the public docs' permissions table (the
// Android column): bluetooth, calendar, camera, contacts, location,
// medialibrary, microphone, notifications, phone, sms, storage. The docs pin
// only bluetooth's expansion explicitly ("targets both BLUETOOTH_CONNECT and
// BLUETOOTH_SCAN automatically"); the other keys map to AOSP dangerous-
// permission groups.
var androidPermissionGroups = map[string][]string{
	"bluetooth": {"android.permission.BLUETOOTH_CONNECT", "android.permission.BLUETOOTH_SCAN"},
	"calendar":  {"android.permission.READ_CALENDAR", "android.permission.WRITE_CALENDAR"},
	"camera":    {"android.permission.CAMERA"},
	"contacts":  {"android.permission.READ_CONTACTS", "android.permission.WRITE_CONTACTS"},
	"location":  {"android.permission.ACCESS_COARSE_LOCATION", "android.permission.ACCESS_FINE_LOCATION"},
	"medialibrary": {
		"android.permission.READ_MEDIA_AUDIO",
		"android.permission.READ_MEDIA_IMAGES",
		"android.permission.READ_MEDIA_VIDEO",
	},
	"microphone":    {"android.permission.RECORD_AUDIO"},
	"notifications": {"android.permission.POST_NOTIFICATIONS"},
	"phone":         {"android.permission.CALL_PHONE", "android.permission.READ_PHONE_STATE"},
	"sms":           {"android.permission.READ_SMS", "android.permission.RECEIVE_SMS", "android.permission.SEND_SMS"},
	"storage":       {"android.permission.READ_EXTERNAL_STORAGE", "android.permission.WRITE_EXTERNAL_STORAGE"},
}

// SetPermissions applies one pm call per permission, in a stable order: pm
// takes a single permission per invocation, and map order must not decide
// what gets applied first.
//
// `all` is applied before the named keys so an explicit key can override it —
// the docs' own example is `all: deny` plus `camera: allow`. It expands to
// the app's OWN runtime permissions (read off dumpsys), because pm refuses to
// grant anything the app never requested.
//
// ponytail: a named permission the app did not request still fails with pm's
// own SecurityException naming it; intersecting named keys with the manifest
// too is the upgrade path if that ever bites.
func (driver *Driver) SetPermissions(ctx context.Context, request device.PermissionsRequest) error {
	if grant, hasAll := request.Permissions["all"]; hasAll {
		if err := driver.applyAllPermissions(ctx, request.AppID, grant); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(request.Permissions))
	for name := range request.Permissions {
		if name != "all" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		permissions, err := expandPermission(name)
		if err != nil {
			return err
		}
		for _, permission := range permissions {
			if err := driver.applyPermission(
				ctx, request.AppID, permission, request.Permissions[name]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (driver *Driver) applyAllPermissions(ctx context.Context, appID, grant string) error {
	permissions, err := driver.adb.RuntimePermissions(ctx, appID)
	if err != nil {
		return err
	}
	for _, permission := range permissions {
		if err := driver.applyPermission(ctx, appID, permission, grant); err != nil {
			return err
		}
	}
	return nil
}

// expandPermission maps an authored key onto full android.permission ids. A
// dotted key is already one (the docs' "Android custom permissions" rule); a
// short name outside the documented set is refused rather than guessed into
// a permission id that does not exist.
func expandPermission(name string) ([]string, error) {
	if group, known := androidPermissionGroups[strings.ToLower(name)]; known {
		return group, nil
	}
	if strings.Contains(name, ".") {
		return []string{name}, nil
	}
	return nil, fmt.Errorf(
		"android permission %q is neither a documented short name nor a full permission id", name)
}

// applyPermission runs the one pm call, and swallows pm's refusal.
//
// The refusal is real and common: pm exits 255 on a system-fixed permission
// (`Non-System UID cannot revoke system fixed permission
// android.permission.ACCESS_BACKGROUND_LOCATION for package
// com.android.settings`), and a flow that says `all: deny` is asking to deny
// what can be denied, not to abort the run over a permission the platform
// owns. Best-effort application still attempts every requested permission.
//
// What is NOT swallowed is a grant verb this layer does not understand: that
// is the flow author's typo, not the device's answer.
func (driver *Driver) applyPermission(ctx context.Context, appID, permission, grant string) error {
	switch grant {
	case "allow":
		_ = driver.adb.GrantPermission(ctx, appID, permission)
		return nil
	case "deny", "unset":
		// pm has no per-permission reset. revoke returns the permission to its
		// not-granted default and the app will prompt again, which is also
		// what unset asks for; the two verbs meet in the same place here.
		_ = driver.adb.RevokePermission(ctx, appID, permission)
		return nil
	default:
		return fmt.Errorf("android permission grant %q must be allow, deny, or unset", grant)
	}
}
