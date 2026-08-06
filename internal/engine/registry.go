package engine

import (
	"context"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

type effectClass uint8

const (
	EffectNone effectClass = iota
	EffectObserved
	EffectDeviceMutation
	EffectHostMutation
	EffectComposite
	EffectArtifact
)

type commandEffect struct {
	effectClass           effectClass
	exactErrorRequest     *exactErrorRequest
	exactErrorPropagation *exactErrorPropagationRequest
	exactErrorDisposition *exactErrorDisposition
	numberOfRuns          int
	numberOfRunsSet       bool
	evaluatedCommand      *model.Command
	logMessages           []string
	insight               string
	aiReasoning           string
	finalizedArtifacts    []device.Artifact
	artifactWrites        []ArtifactWriteRequest
}

type exactErrorRequest struct {
	underlying error
}

type exactErrorDisposition struct {
	published      error
	classification error
}

type exactErrorPropagationRequest struct {
	source *CommandResult
}

type retryErrorClassification struct {
	classification error
	publication    *exactErrorDisposition
}

// commandCompiler converts one immutable command snapshot into a reusable,
// effect-free handler payload. compileContext exposes only prepared structural
// dependencies; runtime services are deliberately unavailable here.
type commandCompiler func(context.Context, compileContext, model.Command) (any, error)

// evaluatedDispatch is the required late-bound boundary between an
// effect-free compiled payload and its executor. The command and value are
// owned snapshots produced immediately before command effects begin.
type evaluatedDispatch struct {
	command model.Command
	value   any
}

type commandEvaluator func(context.Context, evaluationContext, model.Command, any) (evaluatedDispatch, error)

type commandExecutor func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error)

type postActionPolicy uint8

const (
	postActionUnspecified postActionPolicy = iota
	postActionSettle
	postActionNoSettle
)

type exactErrorPolicy uint8

const (
	exactErrorPublicationUnspecified exactErrorPolicy = iota
	exactErrorPublicationPressKey
	exactErrorPublicationRetryPropagation
)

type requiredService uint8

const (
	requiredServiceNone requiredService = iota
	requiredServiceInputGenerator
)

type settleRequestFactory func(evaluatedDispatch) (device.SettleRequest, error)

// pureCompiler adapts command compilers that do not need prepared-flow
// context. A nil function remains nil so registry validation still fails
// closed.
func pureCompiler(compile func(model.Command) (any, error)) commandCompiler {
	if compile == nil {
		return nil
	}
	return func(_ context.Context, _ compileContext, command model.Command) (any, error) {
		return compile(command)
	}
}

func identityEvaluator(
	_ context.Context,
	_ evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	if isNilValue(compiled) {
		compiled = struct{}{}
	}
	return evaluatedDispatch{command: cloneCommand(command), value: compiled}, nil
}

type handlerSpec struct {
	keyword          model.CommandKeyword
	effectClass      effectClass
	postAction       postActionPolicy
	settleRequest    settleRequestFactory
	exactErrorPolicy exactErrorPolicy
	requiredService  requiredService
	compile          commandCompiler
	evaluate         commandEvaluator
	execute          commandExecutor
}

type handlerRegistry struct {
	byKeyword map[model.CommandKeyword]handlerSpec
}

func newHandlerRegistry(specs ...handlerSpec) (handlerRegistry, error) {
	registered := make(map[model.CommandKeyword]handlerSpec, len(specs))
	for _, spec := range specs {
		if strings.TrimSpace(string(spec.keyword)) == "" {
			return handlerRegistry{}, NewConfigurationError("handler keyword must not be blank", nil)
		}
		if _, exists := registered[spec.keyword]; exists {
			return handlerRegistry{}, NewConfigurationError("handler keyword must be unique", nil)
		}
		if spec.compile == nil {
			return handlerRegistry{}, NewConfigurationError("handler compiler must not be nil", nil)
		}
		if spec.evaluate == nil {
			return handlerRegistry{}, NewConfigurationError("handler evaluator must not be nil", nil)
		}
		if spec.execute == nil {
			return handlerRegistry{}, NewConfigurationError("handler executor must not be nil", nil)
		}
		if !validEffectClass(spec.effectClass) {
			return handlerRegistry{}, NewConfigurationError("handler effect class is invalid", nil)
		}
		if err := validatePostActionPolicy(spec); err != nil {
			return handlerRegistry{}, err
		}
		if err := validateExactErrorPolicy(spec); err != nil {
			return handlerRegistry{}, err
		}
		if !validRequiredService(spec.requiredService) {
			return handlerRegistry{}, NewConfigurationError("handler required service is invalid", nil)
		}
		registered[spec.keyword] = spec
	}
	return handlerRegistry{byKeyword: registered}, nil
}

func validRequiredService(service requiredService) bool {
	switch service {
	case requiredServiceNone, requiredServiceInputGenerator:
		return true
	default:
		return false
	}
}

func validateExactErrorPolicy(spec handlerSpec) error {
	switch spec.exactErrorPolicy {
	case exactErrorPublicationUnspecified:
		return nil
	case exactErrorPublicationPressKey:
		if spec.keyword != model.CommandPressKey {
			return NewConfigurationError("exact-error publication is authorized only for pressKey", nil)
		}
		return nil
	case exactErrorPublicationRetryPropagation:
		if spec.keyword != model.CommandRetry {
			return NewConfigurationError("exact-error propagation is authorized only for retry", nil)
		}
		return nil
	default:
		return NewConfigurationError("handler exact-error policy is invalid", nil)
	}
}

func validatePostActionPolicy(spec handlerSpec) error {
	if spec.effectClass != EffectDeviceMutation {
		if spec.postAction != postActionUnspecified || spec.settleRequest != nil {
			return NewConfigurationError("only device-mutation handlers may declare a post-action policy", nil)
		}
		return nil
	}
	switch spec.postAction {
	case postActionSettle:
		if spec.settleRequest == nil {
			return NewConfigurationError("settling device-mutation handler requires a settle request factory", nil)
		}
	case postActionNoSettle:
		if spec.settleRequest != nil {
			return NewConfigurationError("no-settle device-mutation handler must not declare a settle request factory", nil)
		}
	default:
		return NewConfigurationError("device-mutation handler requires an explicit post-action policy", nil)
	}
	return nil
}

func (registry handlerRegistry) lookup(keyword model.CommandKeyword) (handlerSpec, bool) {
	spec, ok := registry.byKeyword[keyword]
	return spec, ok
}

func validEffectClass(class effectClass) bool {
	return class >= EffectObserved && class <= EffectArtifact
}
