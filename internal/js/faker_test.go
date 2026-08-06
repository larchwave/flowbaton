package js

import (
	"context"
	"math/rand"
	"regexp"
	"testing"
)

func TestFakerUsesInjectedDeterministicRandomSource(t *testing.T) {
	t.Parallel()

	evaluate := func(seed int64) string {
		t.Helper()
		factory, err := NewFactory(Config{Random: rand.New(rand.NewSource(seed))})
		if err != nil {
			t.Fatalf("NewFactory() error = %v", err)
		}
		runtime, err := factory.NewRuntime()
		if err != nil {
			t.Fatalf("NewRuntime() error = %v", err)
		}
		defer func() { _ = runtime.Close() }()

		result, err := runtime.Evaluate(context.Background(), EvalRequest{
			Script: "faker.randomInt(10, 20) + ':' + faker.uuid()",
		})
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
		return result.Text
	}

	first := evaluate(41)
	second := evaluate(41)
	if first != second {
		t.Fatalf("same RNG seed produced %q and %q", first, second)
	}
	if matched := regexp.MustCompile(`^(1[0-9]|20):[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(first); !matched {
		t.Fatalf("faker result %q does not contain an in-range integer and RFC 4122 v4 UUID", first)
	}
}
