package ios

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
)

// The eighteen routes of contracts/v0/ios-http.json. Field names and tags are
// the contract's, not Go's conventions: the runner is the other side of a
// frozen wire and this file is where the two spellings meet.

// Key is the pressKey vocabulary.
type Key string

const (
	KeyDelete Key = "delete"
	KeyReturn Key = "return"
	KeyEnter  Key = "enter"
	KeyTab    Key = "tab"
	KeySpace  Key = "space"
	KeyEscape Key = "escape"
)

// Button is the pressButton vocabulary.
type Button string

const (
	ButtonHome Button = "home"
	ButtonLock Button = "lock"
)

// Orientation is the setOrientation vocabulary.
type Orientation string

const (
	OrientationPortrait       Orientation = "portrait"
	OrientationLandscapeLeft  Orientation = "landscapeLeft"
	OrientationLandscapeRight Orientation = "landscapeRight"
	OrientationUpsideDown     Orientation = "upsideDown"
)

// ScreenOrientation is the deviceInfo orientation vocabulary.
type ScreenOrientation string

const (
	ScreenOrientationPortrait       ScreenOrientation = "portrait"
	ScreenOrientationUpsideDown     ScreenOrientation = "portrait-upside-down"
	ScreenOrientationLandscapeLeft  ScreenOrientation = "landscape-left"
	ScreenOrientationLandscapeRight ScreenOrientation = "landscape-right"
)

// SwipeRequest is the v1 swipe, which carries a single optional appId.
type SwipeRequest struct {
	AppID    string  `json:"appId,omitempty"`
	StartX   float64 `json:"startX"`
	StartY   float64 `json:"startY"`
	EndX     float64 `json:"endX"`
	EndY     float64 `json:"endY"`
	Duration float64 `json:"duration"`
}

// SwipeV2Request is the orientation-aware swipe, which carries an appId list.
type SwipeV2Request struct {
	StartX   float64  `json:"startX"`
	StartY   float64  `json:"startY"`
	EndX     float64  `json:"endX"`
	EndY     float64  `json:"endY"`
	Duration float64  `json:"duration"`
	AppIDs   []string `json:"appIds,omitempty"`
}

// TouchRequest taps at a point. Duration is a pointer because its presence is
// what turns the touch into a long press, so an absent one must not be sent as
// a zero.
type TouchRequest struct {
	X        float64  `json:"x"`
	Y        float64  `json:"y"`
	Duration *float64 `json:"duration,omitempty"`
}

// DeviceInfo is the runner's screen geometry, in points and pixels.
type DeviceInfo struct {
	WidthPoints  float64           `json:"widthPoints"`
	HeightPoints float64           `json:"heightPoints"`
	WidthPixels  float64           `json:"widthPixels"`
	HeightPixels float64           `json:"heightPixels"`
	Orientation  ScreenOrientation `json:"orientation"`
}

// Frame is an element's screen-space rectangle.
type Frame struct {
	X      float64 `json:"X"`
	Y      float64 `json:"Y"`
	Width  float64 `json:"Width"`
	Height float64 `json:"Height"`
}

// AXElement is one node of the runner's accessibility hierarchy.
type AXElement struct {
	Identifier          string      `json:"identifier"`
	Frame               Frame       `json:"frame"`
	Value               *string     `json:"value,omitempty"`
	Title               *string     `json:"title,omitempty"`
	Label               string      `json:"label"`
	ElementType         int         `json:"elementType"`
	Enabled             bool        `json:"enabled"`
	HorizontalSizeClass int         `json:"horizontalSizeClass"`
	VerticalSizeClass   int         `json:"verticalSizeClass"`
	PlaceholderValue    *string     `json:"placeholderValue,omitempty"`
	Selected            bool        `json:"selected"`
	HasFocus            bool        `json:"hasFocus"`
	Children            []AXElement `json:"children,omitempty"`
	WindowContextID     float64     `json:"windowContextID"`
	DisplayID           int         `json:"displayID"`
}

// ViewHierarchy is the runner's hierarchy response.
type ViewHierarchy struct {
	AXElement AXElement `json:"axElement"`
	Depth     int       `json:"depth"`
}

// RunningApp returns the first foreground bundle id among appIDs, or the
// springboard when none of them is foreground.
// wireAppIDs makes an app filter safe to marshal.
//
// nil and empty both mean no filter to callers. The wire schema requires an
// array, so normalize nil to [] for every route carrying appIds.
func wireAppIDs(appIDs []string) []string {
	if appIDs == nil {
		return []string{}
	}
	return appIDs
}

func (client *Client) RunningApp(ctx context.Context, appIDs []string) (string, error) {
	var response struct {
		BundleID string `json:"runningAppBundleId"`
	}
	request := struct {
		AppIDs []string `json:"appIds"`
	}{AppIDs: wireAppIDs(appIDs)}
	if _, err := client.do(ctx, http.MethodGet, "/runningApp", "", request, &response); err != nil {
		return "", err
	}
	return response.BundleID, nil
}

func (client *Client) Swipe(ctx context.Context, request SwipeRequest) error {
	_, err := client.do(ctx, http.MethodPost, "/swipe", "", request, nil)
	return err
}

func (client *Client) SwipeV2(ctx context.Context, request SwipeV2Request) error {
	_, err := client.do(ctx, http.MethodPost, "/swipeV2", "", request, nil)
	return err
}

func (client *Client) InputText(ctx context.Context, text string, appIDs []string) error {
	request := struct {
		Text   string   `json:"text"`
		AppIDs []string `json:"appIds"`
	}{Text: text, AppIDs: wireAppIDs(appIDs)}
	_, err := client.do(ctx, http.MethodPost, "/inputText", "", request, nil)
	return err
}

func (client *Client) Touch(ctx context.Context, request TouchRequest) error {
	_, err := client.do(ctx, http.MethodPost, "/touch", "", request, nil)
	return err
}

// Screenshot returns the raw image bytes: JPEG when compressed, PNG otherwise.
func (client *Client) Screenshot(ctx context.Context, compressed bool) ([]byte, error) {
	return client.do(
		ctx, http.MethodGet, "/screenshot", "compressed="+strconv.FormatBool(compressed), nil, nil)
}

func (client *Client) IsScreenStatic(ctx context.Context) (bool, error) {
	var response struct {
		IsScreenStatic bool `json:"isScreenStatic"`
	}
	if _, err := client.do(ctx, http.MethodGet, "/isScreenStatic", "", nil, &response); err != nil {
		return false, err
	}
	return response.IsScreenStatic, nil
}

func (client *Client) PressKey(ctx context.Context, key Key, appIDs []string) error {
	request := struct {
		Key    Key      `json:"key"`
		AppIDs []string `json:"appIds"`
	}{Key: key, AppIDs: wireAppIDs(appIDs)}
	_, err := client.do(ctx, http.MethodPost, "/pressKey", "", request, nil)
	return err
}

func (client *Client) PressButton(ctx context.Context, button Button) error {
	request := struct {
		Button Button `json:"button"`
	}{Button: button}
	_, err := client.do(ctx, http.MethodPost, "/pressButton", "", request, nil)
	return err
}

func (client *Client) EraseText(ctx context.Context, characters int, appIDs []string) error {
	request := struct {
		CharactersToErase int      `json:"charactersToErase"`
		AppIDs            []string `json:"appIds"`
	}{CharactersToErase: characters, AppIDs: wireAppIDs(appIDs)}
	_, err := client.do(ctx, http.MethodPost, "/eraseText", "", request, nil)
	return err
}

func (client *Client) DeviceInfo(ctx context.Context) (DeviceInfo, error) {
	var response DeviceInfo
	if _, err := client.do(ctx, http.MethodGet, "/deviceInfo", "", nil, &response); err != nil {
		return DeviceInfo{}, err
	}
	return response, nil
}

func (client *Client) SetOrientation(ctx context.Context, orientation Orientation) error {
	request := struct {
		Orientation Orientation `json:"orientation"`
	}{Orientation: orientation}
	_, err := client.do(ctx, http.MethodPost, "/setOrientation", "", request, nil)
	return err
}

func (client *Client) SetPermissions(ctx context.Context, permissions map[string]string) error {
	request := struct {
		Permissions map[string]string `json:"permissions"`
	}{Permissions: permissions}
	_, err := client.do(ctx, http.MethodPost, "/setPermissions", "", request, nil)
	return err
}

func (client *Client) ViewHierarchy(
	ctx context.Context,
	appIDs []string,
	excludeKeyboardElements bool,
) (ViewHierarchy, error) {
	request := struct {
		AppIDs                  []string `json:"appIds"`
		ExcludeKeyboardElements bool     `json:"excludeKeyboardElements"`
	}{AppIDs: wireAppIDs(appIDs), ExcludeKeyboardElements: excludeKeyboardElements}
	var response ViewHierarchy
	if _, err := client.do(ctx, http.MethodPost, "/viewHierarchy", "", request, &response); err != nil {
		return ViewHierarchy{}, err
	}
	return response, nil
}

// Status is the health check. A 200 carrying anything other than "ok" is not a
// healthy runner, and reporting it as one would be worse than an error.
func (client *Client) Status(ctx context.Context) error {
	var response struct {
		Status string `json:"status"`
	}
	if _, err := client.do(ctx, http.MethodGet, "/status", "", nil, &response); err != nil {
		return err
	}
	if response.Status != "ok" {
		return fmt.Errorf("ios runner: status is %q, want ok", response.Status)
	}
	return nil
}

func (client *Client) KeyboardVisible(ctx context.Context, appIDs []string) (bool, error) {
	request := struct {
		AppIDs []string `json:"appIds"`
	}{AppIDs: wireAppIDs(appIDs)}
	var response struct {
		IsKeyboardVisible bool `json:"isKeyboardVisible"`
	}
	if _, err := client.do(ctx, http.MethodGet, "/keyboard", "", request, &response); err != nil {
		return false, err
	}
	return response.IsKeyboardVisible, nil
}

func (client *Client) LaunchApp(ctx context.Context, bundleID string) error {
	request := struct {
		BundleID string `json:"bundleId"`
	}{BundleID: bundleID}
	_, err := client.do(ctx, http.MethodPost, "/launchApp", "", request, nil)
	return err
}

func (client *Client) TerminateApp(ctx context.Context, appID string) error {
	request := struct {
		AppID string `json:"appId"`
	}{AppID: appID}
	_, err := client.do(ctx, http.MethodPost, "/terminateApp", "", request, nil)
	return err
}
