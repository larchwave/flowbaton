package device

// IOSRouteV0 is a data-only route descriptor shared by the host client and the
// Swift runner implementation. It describes a contract; it performs no I/O.
type IOSRouteV0 struct {
	Name            string `json:"name"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	RequestLocation string `json:"request_location"`
	RequestSchema   string `json:"request_schema"`
	ResponseSchema  string `json:"response_schema"`
	SuccessStatus   int    `json:"success_status"`
	ErrorSchema     string `json:"error_schema"`
	ErrorStatuses   []int  `json:"error_statuses"`
}

var iosRoutesV0 = []IOSRouteV0{
	newIOSRouteV0("runningApp", "GET", "json_body", "RunningAppRequest", "RunningAppResponse"),
	newIOSRouteV0("swipe", "POST", "json_body", "SwipeRequest", "EmptyResponse"),
	newIOSRouteV0("swipeV2", "POST", "json_body", "SwipeV2Request", "EmptyResponse"),
	newIOSRouteV0("inputText", "POST", "json_body", "InputTextRequest", "EmptyResponse"),
	newIOSRouteV0("touch", "POST", "json_body", "TouchRequest", "EmptyResponse"),
	newIOSRouteV0("screenshot", "GET", "query", "ScreenshotQuery", "ScreenshotResponse"),
	newIOSRouteV0("isScreenStatic", "GET", "none", "NoBody", "ScreenStaticResponse"),
	newIOSRouteV0("pressKey", "POST", "json_body", "PressKeyRequest", "EmptyResponse"),
	newIOSRouteV0("pressButton", "POST", "json_body", "PressButtonRequest", "EmptyResponse"),
	newIOSRouteV0("eraseText", "POST", "json_body", "EraseTextRequest", "EmptyResponse"),
	newIOSRouteV0("deviceInfo", "GET", "none", "NoBody", "DeviceInfoResponse"),
	newIOSRouteV0("setOrientation", "POST", "json_body", "SetOrientationRequest", "EmptyResponse"),
	newIOSRouteV0("setPermissions", "POST", "json_body", "SetPermissionsRequest", "EmptyResponse"),
	newIOSRouteV0("viewHierarchy", "POST", "json_body", "ViewHierarchyRequest", "ViewHierarchyResponse"),
	newIOSRouteV0("status", "GET", "none", "NoBody", "StatusResponse"),
	newIOSRouteV0("keyboard", "GET", "json_body", "KeyboardRequest", "KeyboardResponse"),
	newIOSRouteV0("launchApp", "POST", "json_body", "LaunchAppRequest", "EmptyResponse"),
	newIOSRouteV0("terminateApp", "POST", "json_body", "TerminateAppRequest", "EmptyResponse"),
}

// IOSRoutesContractV0 returns a deep copy so callers cannot mutate the frozen
// process-wide contract.
func IOSRoutesContractV0() []IOSRouteV0 {
	routes := make([]IOSRouteV0, len(iosRoutesV0))
	for index, route := range iosRoutesV0 {
		routes[index] = route
		routes[index].ErrorStatuses = append([]int(nil), route.ErrorStatuses...)
	}
	return routes
}

func newIOSRouteV0(name, method, requestLocation, requestSchema, responseSchema string) IOSRouteV0 {
	return IOSRouteV0{
		Name:            name,
		Method:          method,
		Path:            "/" + name,
		RequestLocation: requestLocation,
		RequestSchema:   requestSchema,
		ResponseSchema:  responseSchema,
		SuccessStatus:   200,
		ErrorSchema:     "ErrorResponse",
		ErrorStatuses:   []int{400, 408, 500},
	}
}
