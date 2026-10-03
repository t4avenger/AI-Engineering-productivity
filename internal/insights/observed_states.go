package insights

import (
	"sort"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func latestObservedStates[T any](
	events []canonical.Event,
	statesFromEvent func(canonical.Event) []T,
	key func(T) string,
	observedAt func(T) time.Time,
	sourceEventID func(T) string,
) []T {
	latest := map[string]T{}
	for _, event := range events {
		for _, state := range statesFromEvent(event) {
			stateKey := key(state)
			current, exists := latest[stateKey]
			if !exists || stateIsLater(state, current, observedAt, sourceEventID) {
				latest[stateKey] = state
			}
		}
	}
	states := make([]T, 0, len(latest))
	for _, state := range latest {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		leftAt, rightAt := observedAt(states[i]), observedAt(states[j])
		if leftAt.Equal(rightAt) {
			return key(states[i]) < key(states[j])
		}
		return leftAt.After(rightAt)
	})
	return states
}

func stateIsLater[T any](state, current T, observedAt func(T) time.Time, sourceEventID func(T) string) bool {
	stateAt, currentAt := observedAt(state), observedAt(current)
	return stateAt.After(currentAt) || stateAt.Equal(currentAt) && sourceEventID(state) > sourceEventID(current)
}
