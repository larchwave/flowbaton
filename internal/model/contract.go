package model

// CommandKeyword is a canonical YAML command discriminator.
type CommandKeyword string

const (
	CommandLaunchApp              CommandKeyword = "launchApp"
	CommandStopApp                CommandKeyword = "stopApp"
	CommandKillApp                CommandKeyword = "killApp"
	CommandClearState             CommandKeyword = "clearState"
	CommandClearKeychain          CommandKeyword = "clearKeychain"
	CommandSetPermissions         CommandKeyword = "setPermissions"
	CommandTapOn                  CommandKeyword = "tapOn"
	CommandDoubleTapOn            CommandKeyword = "doubleTapOn"
	CommandLongPressOn            CommandKeyword = "longPressOn"
	CommandAssertVisible          CommandKeyword = "assertVisible"
	CommandAssertNotVisible       CommandKeyword = "assertNotVisible"
	CommandAssertTrue             CommandKeyword = "assertTrue"
	CommandAssertNoDefectsWithAI  CommandKeyword = "assertNoDefectsWithAI"
	CommandAssertScreenshot       CommandKeyword = "assertScreenshot"
	CommandAssertWithAI           CommandKeyword = "assertWithAI"
	CommandExtractTextWithAI      CommandKeyword = "extractTextWithAI"
	CommandBack                   CommandKeyword = "back"
	CommandHideKeyboard           CommandKeyword = "hideKeyboard"
	CommandPasteText              CommandKeyword = "pasteText"
	CommandScroll                 CommandKeyword = "scroll"
	CommandScrollUntilVisible     CommandKeyword = "scrollUntilVisible"
	CommandInputText              CommandKeyword = "inputText"
	CommandInputRandomText        CommandKeyword = "inputRandomText"
	CommandInputRandomNumber      CommandKeyword = "inputRandomNumber"
	CommandInputRandomEmail       CommandKeyword = "inputRandomEmail"
	CommandInputRandomPersonName  CommandKeyword = "inputRandomPersonName"
	CommandInputRandomCityName    CommandKeyword = "inputRandomCityName"
	CommandInputRandomCountryName CommandKeyword = "inputRandomCountryName"
	CommandInputRandomColorName   CommandKeyword = "inputRandomColorName"
	CommandSwipe                  CommandKeyword = "swipe"
	CommandOpenLink               CommandKeyword = "openLink"
	CommandOpenBrowser            CommandKeyword = "openBrowser"
	CommandPressKey               CommandKeyword = "pressKey"
	CommandEraseText              CommandKeyword = "eraseText"
	CommandAction                 CommandKeyword = "action"
	CommandTakeScreenshot         CommandKeyword = "takeScreenshot"
	CommandExtendedWaitUntil      CommandKeyword = "extendedWaitUntil"
	CommandRunFlow                CommandKeyword = "runFlow"
	CommandSetLocation            CommandKeyword = "setLocation"
	CommandSetOrientation         CommandKeyword = "setOrientation"
	CommandRepeat                 CommandKeyword = "repeat"
	CommandRetry                  CommandKeyword = "retry"
	CommandCopyTextFrom           CommandKeyword = "copyTextFrom"
	CommandSetClipboard           CommandKeyword = "setClipboard"
	CommandRunScript              CommandKeyword = "runScript"
	CommandEvalScript             CommandKeyword = "evalScript"
	CommandWaitForAnimationToEnd  CommandKeyword = "waitForAnimationToEnd"
	CommandTravel                 CommandKeyword = "travel"
	CommandStartRecording         CommandKeyword = "startRecording"
	CommandStopRecording          CommandKeyword = "stopRecording"
	CommandAddMedia               CommandKeyword = "addMedia"
	CommandSetAirplaneMode        CommandKeyword = "setAirplaneMode"
	CommandToggleAirplaneMode     CommandKeyword = "toggleAirplaneMode"

	CommandApplyConfiguration CommandKeyword = "applyConfiguration"
	CommandDefineVariables    CommandKeyword = "defineVariables"
)

var commandKeywordsV0 = []CommandKeyword{
	CommandLaunchApp,
	CommandStopApp,
	CommandKillApp,
	CommandClearState,
	CommandClearKeychain,
	CommandSetPermissions,
	CommandTapOn,
	CommandDoubleTapOn,
	CommandLongPressOn,
	CommandAssertVisible,
	CommandAssertNotVisible,
	CommandAssertTrue,
	CommandAssertNoDefectsWithAI,
	CommandAssertScreenshot,
	CommandAssertWithAI,
	CommandExtractTextWithAI,
	CommandBack,
	CommandHideKeyboard,
	CommandPasteText,
	CommandScroll,
	CommandScrollUntilVisible,
	CommandInputText,
	CommandInputRandomText,
	CommandInputRandomNumber,
	CommandInputRandomEmail,
	CommandInputRandomPersonName,
	CommandInputRandomCityName,
	CommandInputRandomCountryName,
	CommandInputRandomColorName,
	CommandSwipe,
	CommandOpenLink,
	CommandOpenBrowser,
	CommandPressKey,
	CommandEraseText,
	CommandAction,
	CommandTakeScreenshot,
	CommandExtendedWaitUntil,
	CommandRunFlow,
	CommandSetLocation,
	CommandSetOrientation,
	CommandRepeat,
	CommandRetry,
	CommandCopyTextFrom,
	CommandSetClipboard,
	CommandRunScript,
	CommandEvalScript,
	CommandWaitForAnimationToEnd,
	CommandTravel,
	CommandStartRecording,
	CommandStopRecording,
	CommandAddMedia,
	CommandSetAirplaneMode,
	CommandToggleAirplaneMode,
}

var selectorFieldsV0 = []string{
	"text", "id", "width", "height", "tolerance", "optional",
	"retryTapIfNoChange", "waitUntilVisible", "point", "start", "end",
	"below", "above", "leftOf", "rightOf", "containsChild",
	"containsDescendants", "childOf", "traits", "index", "enabled",
	"selected", "checked", "focused", "repeat", "delay",
	"waitToSettleTimeoutMs", "label", "css",
}

var conditionFieldsV0 = []string{"platform", "visible", "notVisible", "true", "label", "optional"}

// ContractDescriptor is the serializable v0 AST contract surface.
type ContractDescriptor struct {
	Version         string           `json:"version"`
	CommandKeywords []CommandKeyword `json:"commandKeywords"`
	SelectorFields  []string         `json:"selectorFields"`
	ConditionFields []string         `json:"conditionFields"`
}

// ContractV0 returns a defensive copy of the frozen v0 AST descriptor.
func ContractV0() ContractDescriptor {
	return ContractDescriptor{
		Version:         ASTVersionV0,
		CommandKeywords: append([]CommandKeyword(nil), commandKeywordsV0...),
		SelectorFields:  append([]string(nil), selectorFieldsV0...),
		ConditionFields: append([]string(nil), conditionFieldsV0...),
	}
}

// CommandKeywords returns the canonical 53-keyword catalog in contract order.
func CommandKeywords() []CommandKeyword {
	return append([]CommandKeyword(nil), commandKeywordsV0...)
}
