// Package device defines the platform-neutral device contract.
//
// Version v0 is deliberately declarations-only: session selection, acquisition,
// transport, and platform behavior belong to later packages and slices.
package device

import (
	"context"
	"errors"
)

const ContractVersionV0 = "v0"

// ErrUnsupported is what a driver returns for an operation its platform cannot
// perform. The Driver surface is platform-neutral, so every implementation has
// methods it cannot answer; the sentinel is what lets a caller tell "this
// platform will never do that" apart from "that failed on the device".
//
// An implementation that returns this for an operation must also report the
// operation unsupported in Capabilities, so preflight can refuse a flow before
// it runs rather than halfway through.
var ErrUnsupported = errors.New("device: operation not supported on this platform")

// RetryableError is a driver error that can say whether resending the same
// request could plausibly succeed. A driver answering "this cannot work as
// posed" -- a runner precondition, say -- implements it, so a
// platform-neutral caller can tell a condition that will repeat from one
// worth another attempt without importing the platform package.
//
// Implementing it is optional. A driver whose errors do not answer the
// question gets the older behavior: a caller retries or reports, and cannot
// tell the two apart. Today internal/ios answers it, because its runner
// refuses a request whose precondition fails. internal/android does not, and
// nothing is lost by that yet: its agent reads the accessibility hierarchy
// through UiAutomation across every window, so it has no foreground
// precondition to refuse on.
type RetryableError interface {
	error
	Retryable() bool
}

type Platform string
type Direction string
type Orientation string
type KeyCode string
type Browser string
type CaptureID string

// Driver is the frozen v0 platform-neutral behavior surface. Implementations
// must preserve context cancellation and may not reinterpret a nil settling
// result as proof that the device settled.
type Driver interface {
	Name() string
	Open(context.Context) error
	Close(context.Context) error
	DeviceInfo(context.Context) (DeviceInfo, error)
	LaunchApp(context.Context, LaunchAppRequest) error
	StopApp(context.Context, AppRequest) error
	KillApp(context.Context, AppRequest) error
	ClearAppState(context.Context, AppRequest) error
	ClearKeychain(context.Context) error
	Tap(context.Context, TapRequest) error
	LongPress(context.Context, LongPressRequest) error
	PressKey(context.Context, PressKeyRequest) error
	ContentDescriptor(context.Context, ContentDescriptorRequest) (TreeNode, error)
	ScrollVertical(context.Context, ScrollVerticalRequest) error
	IsKeyboardVisible(context.Context, KeyboardRequest) (bool, error)
	Swipe(context.Context, SwipeRequest) error
	BackPress(context.Context) error
	InputText(context.Context, InputTextRequest) error
	OpenLink(context.Context, OpenLinkRequest) error
	HideKeyboard(context.Context) error
	TakeScreenshot(context.Context, ScreenshotRequest) ([]byte, error)
	StartScreenRecording(context.Context, ScreenRecordingRequest) (CaptureID, error)
	SetLocation(context.Context, Location) error
	SetOrientation(context.Context, Orientation) error
	EraseText(context.Context, EraseTextRequest) error
	SetProxy(context.Context, Proxy) error
	ResetProxy(context.Context) error
	IsShutdown(context.Context) (bool, error)
	WaitUntilScreenIsStatic(context.Context, ScreenStaticRequest) (bool, error)
	WaitForAppToSettle(context.Context, SettleRequest) (*ViewHierarchy, error)
	Capabilities() Capabilities
	SetPermissions(context.Context, PermissionsRequest) error
	AddMedia(context.Context, AddMediaRequest) error
	IsAirplaneModeEnabled(context.Context) (bool, error)
	SetAirplaneMode(context.Context, AirplaneModeRequest) error
	SetAndroidChromeDevToolsEnabled(context.Context, ChromeDevToolsRequest) error
	QueryOnDeviceElements(context.Context, QueryRequest) ([]TreeNode, error)
	StartDeviceLogCapture(context.Context, DeviceLogRequest) (CaptureID, error)
	StopDeviceLogCapture(context.Context, CaptureID) ([]Artifact, error)
	CollectCrashArtifacts(context.Context, ArtifactRequest) ([]Artifact, error)
}

// RuntimeRequirements are the command-level facts a prepared run needs from
// its selected device. They are passed only to optional RuntimePreflighters,
// before Driver.Open, so device-version constraints can fail before installing
// helpers, granting permissions, or otherwise mutating the target.
type RuntimeRequirements struct {
	Commands []string
}

// RuntimePreflighter is an optional, read-only extension to Driver. A driver
// implements it when static platform capabilities are not enough to decide
// support, such as an operation that requires a minimum device OS version.
// Implementations must not mutate the device.
type RuntimePreflighter interface {
	PreflightRuntime(context.Context, RuntimeRequirements) error
}

// OrientationReader is an optional read-only extension used when a caller
// needs the device's actual rotation alongside captured screen content.
type OrientationReader interface {
	CurrentOrientation(context.Context) (Orientation, error)
}

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Bounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type TreeNode struct {
	Attributes map[string]string `json:"attributes"`
	Children   []TreeNode        `json:"children,omitempty"`
	Clickable  *bool             `json:"clickable,omitempty"`
	Enabled    *bool             `json:"enabled,omitempty"`
	Focused    *bool             `json:"focused,omitempty"`
	Checked    *bool             `json:"checked,omitempty"`
	Selected   *bool             `json:"selected,omitempty"`
}

type UiElement struct {
	Node   TreeNode `json:"node"`
	Bounds Bounds   `json:"bounds"`
}

type ViewHierarchy struct {
	Root TreeNode `json:"root"`
}

type DeviceInfo struct {
	Platform     Platform `json:"platform"`
	WidthPixels  int      `json:"width_pixels"`
	HeightPixels int      `json:"height_pixels"`
	WidthGrid    int      `json:"width_grid"`
	HeightGrid   int      `json:"height_grid"`
}

type LaunchArgument struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type LaunchAppRequest struct {
	AppID     string           `json:"app_id"`
	Arguments []LaunchArgument `json:"arguments,omitempty"`
}

type AppRequest struct {
	AppID string `json:"app_id"`
}

type TapRequest struct {
	Point Point `json:"point"`
}

type LongPressRequest struct {
	Point          Point `json:"point"`
	DurationMillis int64 `json:"duration_millis"`
}

type PressKeyRequest struct {
	Code   KeyCode  `json:"code"`
	AppIDs []string `json:"app_ids,omitempty"`
}

type ContentDescriptorRequest struct {
	AppIDs                  []string `json:"app_ids,omitempty"`
	ExcludeKeyboardElements bool     `json:"exclude_keyboard_elements"`
}

type ScrollVerticalRequest struct {
	Direction    Direction `json:"direction"`
	Amount       float64   `json:"amount"`
	ElementPoint *Point    `json:"element_point,omitempty"`
}

type KeyboardRequest struct {
	AppIDs []string `json:"app_ids,omitempty"`
}

// SwipeRequest represents the three v0 swipe shapes. Start/End, Direction,
// and ElementPoint are validated by the caller as mutually compatible modes.
type SwipeRequest struct {
	Start          *Point    `json:"start,omitempty"`
	End            *Point    `json:"end,omitempty"`
	Direction      Direction `json:"direction,omitempty"`
	ElementPoint   *Point    `json:"element_point,omitempty"`
	DurationMillis int64     `json:"duration_millis"`
}

type InputTextRequest struct {
	Text   string   `json:"text"`
	AppIDs []string `json:"app_ids,omitempty"`
}

type OpenLinkRequest struct {
	Link       string  `json:"link"`
	AppID      string  `json:"app_id,omitempty"`
	AutoVerify bool    `json:"auto_verify"`
	Browser    Browser `json:"browser,omitempty"`
}

type ScreenshotRequest struct {
	Compressed bool `json:"compressed"`
}

type ScreenRecordingRequest struct {
	OutputPath string `json:"output_path"`
}

type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type EraseTextRequest struct {
	CharactersToErase uint32   `json:"characters_to_erase"`
	AppIDs            []string `json:"app_ids,omitempty"`
}

type Proxy struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

type ScreenStaticRequest struct {
	TimeoutMillis int64 `json:"timeout_millis"`
}

// SettleRequest models optional inputs explicitly. A nil result from
// WaitForAppToSettle means settling could not be confirmed.
type SettleRequest struct {
	InitialHierarchy *ViewHierarchy `json:"initial_hierarchy,omitempty"`
	AppID            string         `json:"app_id,omitempty"`
	TimeoutMillis    *int64         `json:"timeout_millis,omitempty"`
}

type Capabilities struct {
	Platform Platform        `json:"platform"`
	Features map[string]bool `json:"features"`
}

type PermissionsRequest struct {
	AppID       string            `json:"app_id"`
	Permissions map[string]string `json:"permissions"`
}

type MediaFile struct {
	Path string `json:"path"`
}

type AddMediaRequest struct {
	Files []MediaFile `json:"files"`
}

type AirplaneModeRequest struct {
	Enabled bool `json:"enabled"`
}

type ChromeDevToolsRequest struct {
	Enabled bool `json:"enabled"`
}

type QueryRequest struct {
	Expression string   `json:"expression"`
	AppIDs     []string `json:"app_ids,omitempty"`
}

type DeviceLogRequest struct {
	OutputDirectory string `json:"output_directory"`
	AppID           string `json:"app_id,omitempty"`
}

type ArtifactRequest struct {
	OutputDirectory string `json:"output_directory"`
	AppID           string `json:"app_id,omitempty"`
}

type Artifact struct {
	Kind     string            `json:"kind"`
	Path     string            `json:"path"`
	Metadata map[string]string `json:"metadata,omitempty"`
}
