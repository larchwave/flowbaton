package pbwire

// Full gRPC method paths for every rpc of the frozen FlowBatonDriver service —
// the single source the Android driver imports. contract_test.go holds these
// against proto/flowbaton_android.proto in both directions.
const (
	MethodDeviceInfo                  = "/flowbaton_android.FlowBatonDriver/deviceInfo"
	MethodViewHierarchy               = "/flowbaton_android.FlowBatonDriver/viewHierarchy"
	MethodScreenshot                  = "/flowbaton_android.FlowBatonDriver/screenshot"
	MethodTap                         = "/flowbaton_android.FlowBatonDriver/tap"
	MethodInputText                   = "/flowbaton_android.FlowBatonDriver/inputText"
	MethodEraseAllText                = "/flowbaton_android.FlowBatonDriver/eraseAllText"
	MethodSetLocation                 = "/flowbaton_android.FlowBatonDriver/setLocation"
	MethodIsWindowUpdating            = "/flowbaton_android.FlowBatonDriver/isWindowUpdating"
	MethodLaunchApp                   = "/flowbaton_android.FlowBatonDriver/launchApp"
	MethodAddMedia                    = "/flowbaton_android.FlowBatonDriver/addMedia"
	MethodEnableMockLocationProviders = "/flowbaton_android.FlowBatonDriver/enableMockLocationProviders"
	MethodDisableLocationUpdates      = "/flowbaton_android.FlowBatonDriver/disableLocationUpdates"
)

// StreamAddMedia marks addMedia as the service's only client-streaming rpc:
// the driver opens a stream for it and unary-calls everything else.
const StreamAddMedia = MethodAddMedia

// MethodByRPC maps each rpc name in the frozen proto to its full method path.
// It returns a fresh map so no caller can edit the registry under another.
func MethodByRPC() map[string]string {
	return map[string]string{
		"deviceInfo":                  MethodDeviceInfo,
		"viewHierarchy":               MethodViewHierarchy,
		"screenshot":                  MethodScreenshot,
		"tap":                         MethodTap,
		"inputText":                   MethodInputText,
		"eraseAllText":                MethodEraseAllText,
		"setLocation":                 MethodSetLocation,
		"isWindowUpdating":            MethodIsWindowUpdating,
		"launchApp":                   MethodLaunchApp,
		"addMedia":                    MethodAddMedia,
		"enableMockLocationProviders": MethodEnableMockLocationProviders,
		"disableLocationUpdates":      MethodDisableLocationUpdates,
	}
}

// IsClientStreaming reports whether method is a client-streaming rpc.
func IsClientStreaming(method string) bool {
	return method == StreamAddMedia
}
