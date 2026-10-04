package inspect

import (
	"go/types"
	"testing"
)

// TestComparableAnswersFromTheType pins the rule the assertion is chosen by:
// the type decides, and a checker that could not answer falls to deep
// equality, which is right for both kinds of value and wrong for neither.
func TestComparableAnswersFromTheType(t *testing.T) {
	str := types.Typ[types.String]
	tests := []struct {
		name string
		in   *Target
		want bool
	}{
		{"nil target — the checker could not answer", nil, false},
		{"no type", &Target{}, false},
		{"string", &Target{Type: str}, true},
		{"pointer", &Target{Type: types.NewPointer(str)}, true},
		{"slice", &Target{Type: types.NewSlice(str)}, false},
		{"map", &Target{Type: types.NewMap(str, str)}, false},
		{"struct of comparables", &Target{Type: types.NewStruct([]*types.Var{
			types.NewField(0, nil, "A", str, false),
		}, nil)}, true},
		{"struct holding a slice", &Target{Type: types.NewStruct([]*types.Var{
			types.NewField(0, nil, "A", types.NewSlice(str), false),
		}, nil)}, false},
		// A multi-value call has no single value to compare at all.
		{"tuple", &Target{Type: str, Tuple: []types.Type{str, str}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Comparable(tc.in); got != tc.want {
				t.Errorf("Comparable = %v, want %v", got, tc.want)
			}
		})
	}
}
